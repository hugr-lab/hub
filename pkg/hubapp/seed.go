package hubapp

import "context"

func (a *HubApp) seedAgentTypes(ctx context.Context) {
	types := []struct {
		ID, DisplayName, Description, Image string
	}{
		{"data-analyst", "Data Analyst", "Hugr data exploration agent with discovery, query, and visualization skills", "hugr-lab/hub-agent:latest"},
		{"openclaw", "OpenClaw Agent", "Third-party OpenClaw agent runtime using Hub Service OpenAI-compatible endpoint", "openclaw/agent:latest"},
	}

	for _, t := range types {
		// Check if exists
		res, err := a.client.Query(ctx,
			`query($id: String!) { hub { db { agent_types(filter: { id: { eq: $id } }) { id } } } }`,
			map[string]any{"id": t.ID},
		)
		if err != nil {
			a.logger.Warn("failed to check agent type", "id", t.ID, "error", err)
			continue
		}
		defer res.Close()
		if res.Err() != nil {
			a.logger.Warn("failed to check agent type", "id", t.ID, "error", res.Err())
			continue
		}

		var existing []struct{ ID string `json:"id"` }
		_ = res.ScanData("hub.db.agent_types", &existing)
		if len(existing) > 0 {
			a.logger.Info("agent type already exists, skipping", "id", t.ID)
			continue
		}

		// Insert
		res2, err := a.client.Query(ctx,
			`mutation($id: String!, $name: String!, $desc: String!, $img: String!) {
				hub { db { insert_agent_types(
					data: { id: $id, display_name: $name, description: $desc, image: $img }
				) { id } } }
			}`,
			map[string]any{"id": t.ID, "name": t.DisplayName, "desc": t.Description, "img": t.Image},
		)
		if err != nil {
			a.logger.Warn("failed to seed agent type", "id", t.ID, "error", err)
			continue
		}
		defer res2.Close()
		if res2.Err() != nil {
			a.logger.Warn("seed agent type error", "id", t.ID, "error", res2.Err())
			continue
		}
		a.logger.Info("agent type seeded", "id", t.ID)
	}
}
