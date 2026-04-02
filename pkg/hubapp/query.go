package hubapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/hugr-lab/query-engine/client"
	"github.com/hugr-lab/query-engine/types"
)

// QueryHugr executes a GraphQL query, checks errors, and returns the response.
// Caller MUST call res.Close() when done.
func QueryHugr(ctx context.Context, c *client.Client, query string, vars map[string]any) (*types.Response, error) {
	res, err := c.Query(ctx, query, vars)
	if err != nil {
		return nil, err
	}
	if res.Err() != nil {
		defer res.Close()
		return nil, fmt.Errorf("graphql errors: %s", formatErrors(res))
	}
	return res, nil
}

// ExecHugr executes a mutation, checks errors, closes response. Fire-and-forget.
func ExecHugr(ctx context.Context, c *client.Client, query string, vars map[string]any) error {
	res, err := c.Query(ctx, query, vars)
	if err != nil {
		return err
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("graphql errors: %s", formatErrors(res))
	}
	return nil
}

func formatErrors(res *types.Response) string {
	if res.Errors == nil {
		return ""
	}
	var msgs []string
	for _, e := range res.Errors {
		msgs = append(msgs, e.Message)
	}
	return strings.Join(msgs, "; ")
}
