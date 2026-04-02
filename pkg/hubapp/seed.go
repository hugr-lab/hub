package hubapp

import (
	"context"
	"log/slog"

	"github.com/hugr-lab/query-engine/client"
)

// seedAgentTypes inserts default agent types if not already present.
func seedAgentTypes(ctx context.Context, c *client.Client) {
	types := []struct {
		ID          string
		DisplayName string
		Description string
		Image       string
	}{
		{
			ID:          "data-analyst",
			DisplayName: "Data Analyst",
			Description: "Hugr data exploration agent with discovery, query, and visualization skills",
			Image:       "hugr-lab/hub-agent:latest",
		},
		{
			ID:          "openclaw",
			DisplayName: "OpenClaw Agent",
			Description: "Third-party OpenClaw agent runtime using Hub Service OpenAI-compatible endpoint",
			Image:       "openclaw/agent:latest",
		},
	}

	for _, t := range types {
		res, err := c.Query(ctx,
			`mutation($id: String!, $name: String!, $desc: String!, $img: String!) {
				hub { hub { insert_agent_types(
					data: { id: $id, display_name: $name, description: $desc, image: $img }
				) { id } } }
			}`,
			map[string]any{"id": t.ID, "name": t.DisplayName, "desc": t.Description, "img": t.Image},
		)
		if err != nil {
			slog.Warn("failed to seed agent type", "id", t.ID, "error", err)
			continue
		}
		_ = res
		slog.Info("agent type seeded", "id", t.ID)
	}
}
