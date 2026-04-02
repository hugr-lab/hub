package llmrouter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hugr-lab/query-engine/client"
)

// BudgetChecker enforces token limits per user/role/global.
type BudgetChecker struct {
	hugrClient *client.Client
	logger     *slog.Logger
}

func NewBudgetChecker(hugrClient *client.Client, logger *slog.Logger) *BudgetChecker {
	return &BudgetChecker{hugrClient: hugrClient, logger: logger}
}

// Check verifies the user has budget remaining for the given provider.
func (b *BudgetChecker) Check(ctx context.Context, userID, providerID string) error {
	// Query budgets for this user (most specific scope wins: user > role > global)
	scopes := []string{
		fmt.Sprintf("user:%s", userID),
		"global",
		// TODO: add role scope from user lookup
	}

	for _, scope := range scopes {
		budget, err := b.getBudget(ctx, scope, providerID)
		if err != nil || budget == nil {
			continue
		}

		usage, err := b.getUsage(ctx, userID, providerID, budget.Period)
		if err != nil {
			b.logger.Warn("failed to get usage", "user", userID, "error", err)
			continue
		}

		if budget.MaxTokensOut > 0 && usage.TokensOut >= budget.MaxTokensOut {
			return fmt.Errorf("output token limit reached (%d/%d for %s period)",
				usage.TokensOut, budget.MaxTokensOut, budget.Period)
		}

		if budget.MaxTokensIn > 0 && usage.TokensIn >= budget.MaxTokensIn {
			return fmt.Errorf("input token limit reached (%d/%d for %s period)",
				usage.TokensIn, budget.MaxTokensIn, budget.Period)
		}

		if budget.MaxRequests > 0 && usage.Requests >= budget.MaxRequests {
			return fmt.Errorf("request limit reached (%d/%d for %s period)",
				usage.Requests, budget.MaxRequests, budget.Period)
		}

		return nil // within budget
	}

	return nil // no budget defined = unlimited
}

// RecordUsage writes usage to hub.llm_usage via Hugr GraphQL.
func (b *BudgetChecker) RecordUsage(ctx context.Context, userID, providerID string, tokensIn, tokensOut int) {
	periodKey := currentPeriodKey("hour")

	_, err := b.hugrClient.Query(ctx,
		`mutation($uid: String!, $pid: String!, $tin: Int!, $tout: Int!, $pk: String!) {
			hub { hub { insert_llm_usage(data: {
				user_id: $uid
				provider_id: $pid
				tokens_in: $tin
				tokens_out: $tout
				period_key: $pk
			}) { id } } }
		}`,
		map[string]any{
			"uid":  userID,
			"pid":  providerID,
			"tin":  tokensIn,
			"tout": tokensOut,
			"pk":   periodKey,
		},
	)
	if err != nil {
		b.logger.Warn("failed to record LLM usage", "user", userID, "error", err)
	}
}

type budgetRule struct {
	Period       string `json:"period"`
	MaxTokensIn  int64  `json:"max_tokens_in"`
	MaxTokensOut int64  `json:"max_tokens_out"`
	MaxRequests  int    `json:"max_requests"`
}

type usageSummary struct {
	TokensIn  int64
	TokensOut int64
	Requests  int
}

func (b *BudgetChecker) getBudget(ctx context.Context, scope, providerID string) (*budgetRule, error) {
	gql := fmt.Sprintf(`{ hub { hub { llm_budgets(
		filter: { scope: { eq: "%s" } }
		limit: 1
	) { period max_tokens_in max_tokens_out max_requests } } } }`, scope)

	res, err := b.hugrClient.Query(ctx, gql, nil)
	if err != nil {
		return nil, err
	}

	var budgets []budgetRule
	if err := res.ScanData("hub.hub.llm_budgets", &budgets); err != nil || len(budgets) == 0 {
		return nil, nil
	}

	return &budgets[0], nil
}

func (b *BudgetChecker) getUsage(ctx context.Context, userID, providerID, period string) (usageSummary, error) {
	periodKey := currentPeriodKey(period)

	gql := fmt.Sprintf(`{ hub { hub { llm_usage_aggregation(
		filter: { user_id: { eq: "%s" }, period_key: { eq: "%s" } }
	) { aggregations { tokens_in { sum } tokens_out { sum } _rows_count } } } } }`,
		userID, periodKey)

	res, err := b.hugrClient.Query(ctx, gql, nil)
	if err != nil {
		return usageSummary{}, err
	}

	var agg []struct {
		Aggregations struct {
			TokensIn  struct{ Sum *int64 } `json:"tokens_in"`
			TokensOut struct{ Sum *int64 } `json:"tokens_out"`
			RowsCount int                  `json:"_rows_count"`
		} `json:"aggregations"`
	}
	if err := res.ScanData("hub.hub.llm_usage_aggregation", &agg); err != nil || len(agg) == 0 {
		return usageSummary{}, nil
	}

	s := usageSummary{Requests: agg[0].Aggregations.RowsCount}
	if agg[0].Aggregations.TokensIn.Sum != nil {
		s.TokensIn = *agg[0].Aggregations.TokensIn.Sum
	}
	if agg[0].Aggregations.TokensOut.Sum != nil {
		s.TokensOut = *agg[0].Aggregations.TokensOut.Sum
	}
	return s, nil
}

func currentPeriodKey(period string) string {
	now := time.Now().UTC()
	switch period {
	case "hour":
		return fmt.Sprintf("%s/hour/%d", now.Format("2006-01-02"), now.Hour())
	case "day":
		return now.Format("2006-01-02") + "/day"
	case "month":
		return now.Format("2006-01") + "/month"
	default:
		return now.Format("2006-01-02") + "/day"
	}
}
