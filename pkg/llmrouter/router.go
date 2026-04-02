package llmrouter

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/hugr-lab/query-engine/client"
)

// Message represents a chat message for LLM completion.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest is the input to the LLM router.
type CompletionRequest struct {
	Provider  string    `json:"provider"`   // provider ID (empty = default)
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
	UserID    string    `json:"user_id"`
}

// CompletionResponse is the LLM output.
type CompletionResponse struct {
	Content   string `json:"content"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

// ProviderConfig loaded from hub.llm_providers table.
type ProviderConfig struct {
	ID                  string `json:"id"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	BaseURL             string `json:"base_url"`
	APIKeyRef           string `json:"api_key_ref"`
	MaxTokensPerRequest int    `json:"max_tokens_per_request"`
}

// Provider is the interface for LLM provider implementations.
type Provider interface {
	Complete(ctx context.Context, cfg ProviderConfig, req CompletionRequest) (CompletionResponse, error)
}

// Router dispatches LLM requests to configured providers with budget enforcement.
type Router struct {
	hugrClient *client.Client
	providers  map[string]Provider // protocol → implementation
	budget     *BudgetChecker
	logger     *slog.Logger
}

func New(hugrClient *client.Client, logger *slog.Logger) *Router {
	r := &Router{
		hugrClient: hugrClient,
		providers: map[string]Provider{
			"anthropic":       &AnthropicProvider{},
			"openai":          &OpenAIProvider{},
			"openai-compatible": &OpenAIProvider{}, // same protocol, different base_url
			"gemini":          &GeminiProvider{},
		},
		budget: NewBudgetChecker(hugrClient, logger),
		logger: logger,
	}
	return r
}

// Complete routes a request to the appropriate LLM provider.
func (r *Router) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	// 1. Resolve provider config from hub.llm_providers
	cfg, err := r.resolveProvider(ctx, req.Provider)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("resolve provider: %w", err)
	}

	// 2. Check budget
	if err := r.budget.Check(ctx, req.UserID, cfg.ID); err != nil {
		return CompletionResponse{}, fmt.Errorf("budget exceeded: %w", err)
	}

	// 3. Apply max tokens
	if req.MaxTokens == 0 || req.MaxTokens > cfg.MaxTokensPerRequest {
		req.MaxTokens = cfg.MaxTokensPerRequest
	}

	// 4. Dispatch to provider
	impl, ok := r.providers[cfg.Provider]
	if !ok {
		return CompletionResponse{}, fmt.Errorf("unknown provider protocol: %s", cfg.Provider)
	}

	resp, err := impl.Complete(ctx, cfg, req)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("provider %s: %w", cfg.ID, err)
	}

	resp.Provider = cfg.ID
	resp.Model = cfg.Model

	// 5. Record usage
	r.budget.RecordUsage(ctx, req.UserID, cfg.ID, resp.TokensIn, resp.TokensOut)

	r.logger.Info("llm completion",
		"user", req.UserID,
		"provider", cfg.ID,
		"model", cfg.Model,
		"tokens_in", resp.TokensIn,
		"tokens_out", resp.TokensOut,
	)

	return resp, nil
}

// resolveProvider looks up provider config from hub.llm_providers.
func (r *Router) resolveProvider(ctx context.Context, providerID string) (ProviderConfig, error) {
	filter := `{ enabled: { eq: true } }`
	if providerID != "" {
		filter = fmt.Sprintf(`{ id: { eq: "%s" }, enabled: { eq: true } }`, providerID)
	}

	gql := fmt.Sprintf(`{ hub { hub { llm_providers(filter: %s, limit: 1) {
		id provider model base_url api_key_ref max_tokens_per_request
	} } } }`, filter)

	res, err := r.hugrClient.Query(ctx, gql, nil)
	if err != nil {
		return ProviderConfig{}, fmt.Errorf("query providers: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return ProviderConfig{}, fmt.Errorf("query providers graphql error: %w", res.Err())
	}

	var providers []ProviderConfig
	if err := res.ScanData("hub.hub.llm_providers", &providers); err != nil {
		return ProviderConfig{}, fmt.Errorf("scan providers: %w", err)
	}

	if len(providers) == 0 {
		return ProviderConfig{}, fmt.Errorf("no enabled provider found (requested: %q)", providerID)
	}

	cfg := providers[0]
	if cfg.MaxTokensPerRequest == 0 {
		cfg.MaxTokensPerRequest = 4096
	}

	// Resolve API key from secret reference
	if cfg.APIKeyRef != "" {
		cfg.APIKeyRef = resolveSecret(cfg.APIKeyRef)
	}

	return cfg, nil
}

// resolveSecret resolves ${secret:ENV_VAR} references to env values.
func resolveSecret(ref string) string {
	const prefix = "${secret:"
	if len(ref) > len(prefix)+1 && ref[:len(prefix)] == prefix && ref[len(ref)-1] == '}' {
		key := ref[len(prefix) : len(ref)-1]
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
	}
	return ref
}
