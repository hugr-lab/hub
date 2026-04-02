package hubapp

import "context"

// seedAgentTypes inserts default agent types if not already present.
func (a *HubApp) seedAgentTypes(ctx context.Context) {
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
		res, err := a.client.Query(ctx,
			`mutation($id: String!, $name: String!, $desc: String!, $img: String!) {
				hub { hub { insert_agent_types(
					data: { id: $id, display_name: $name, description: $desc, image: $img }
				) { id } } }
			}`,
			map[string]any{"id": t.ID, "name": t.DisplayName, "desc": t.Description, "img": t.Image},
		)
		if err != nil {
			a.logger.Warn("failed to seed agent type (may already exist)", "id", t.ID, "error", err)
			continue
		}
		defer res.Close()
		if res.Err() != nil {
			a.logger.Warn("seed agent type error", "id", t.ID, "error", res.Err())
			continue
		}
		a.logger.Info("agent type seeded", "id", t.ID)
	}
}
