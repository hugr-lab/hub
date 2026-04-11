package hubapp

import "context"

func (a *HubApp) seedAgentTypes(ctx context.Context) {
	types := []struct {
		ID, DisplayName, Description, Image string
	}{
		{"data-analyst", "Data Analyst", "Hugr data exploration agent with discovery, query, and visualization skills", "hub-agent:latest"},
		{"openclaw", "OpenClaw Agent", "Third-party OpenClaw agent runtime using Hub Service OpenAI-compatible endpoint", "openclaw/agent:latest"},
	}

	for _, t := range types {
		a.seedOneAgentType(ctx, t.ID, t.DisplayName, t.Description, t.Image)
	}
}

// seedOneAgentType seeds a single agent type. Extracted from the loop so each
// iteration gets its own deferred Close() and we don't accumulate result
// handles until seedAgentTypes returns.
func (a *HubApp) seedOneAgentType(ctx context.Context, id, displayName, description, image string) {
	// Check if exists
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { agent_types(filter: { id: { eq: $id } }) { id } } } }`,
		map[string]any{"id": id},
	)
	if err != nil {
		a.logger.Warn("failed to check agent type", "id", id, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		a.logger.Warn("failed to check agent type", "id", id, "error", res.Err())
		return
	}

	var existing []struct {
		ID string `json:"id"`
	}
	_ = res.ScanData("hub.db.agent_types", &existing)
	if len(existing) > 0 {
		a.logger.Info("agent type already exists, skipping", "id", id)
		return
	}

	// Insert
	res2, err := a.client.Query(ctx,
		`mutation($id: String!, $name: String!, $desc: String!, $img: String!) {
			hub { db { insert_agent_types(
				data: { id: $id, display_name: $name, description: $desc, image: $img }
			) { id } } }
		}`,
		map[string]any{"id": id, "name": displayName, "desc": description, "img": image},
	)
	if err != nil {
		a.logger.Warn("failed to seed agent type", "id", id, "error", err)
		return
	}
	defer res2.Close()
	if res2.Err() != nil {
		a.logger.Warn("seed agent type error", "id", id, "error", res2.Err())
		return
	}
	a.logger.Info("agent type seeded", "id", id)
}
