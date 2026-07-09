package hubapp

import "context"

// defaultAgentImage is the container image stamped into a seeded agent_type's
// orchestration block until an admin authors a real one. The hugen image is
// built in M3 (Dockerfile); HB4's DockerRuntime reads it back via
// agentmgr.ImageFromConfig(agent_type.config).
const defaultAgentImage = "hugen:latest"

// seedAgentTypes seeds the default agent types into the Agent DB
// (hub.agent.db.agent_types — the hugen-owned canon; the legacy platform
// hub.db.agent_types is dropped in S4). A type's `config` is the agent runtime
// config hugen reads via agent_info (agent_type.config ⊕ config_override); its
// `orchestration` sub-block is hub-only spawn metadata (image/cpu/mem/mounts)
// that hugen's config loader ignores (it pulls only the keys it knows) and
// HB4's DockerRuntime reads for the container spawn.
func (a *HubApp) seedAgentTypes(ctx context.Context) {
	types := []struct {
		ID, Name, Description string
		Config                map[string]any
	}{
		{
			ID:          "data-analyst",
			Name:        "Data Analyst",
			Description: "Hugr data-exploration agent: discovery, query, and analysis skills over the mesh.",
			Config: map[string]any{
				"orchestration": map[string]any{"image": defaultAgentImage},
			},
		},
	}

	for _, t := range types {
		a.seedOneAgentType(ctx, t.ID, t.Name, t.Description, t.Config)
	}
}

// seedOneAgentType seeds a single agent type into the Agent DB (idempotent —
// existing types are left untouched so admin edits survive a restart). Extracted
// from the loop so each iteration gets its own deferred Close().
func (a *HubApp) seedOneAgentType(ctx context.Context, id, name, description string, config map[string]any) {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { agent { db { agent_types(filter: { id: { eq: $id } } limit: 1) { id } } } } }`,
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
	_ = res.ScanData("hub.agent.db.agent_types", &existing)
	if len(existing) > 0 {
		a.logger.Info("agent type already exists, skipping", "id", id)
		return
	}

	res2, err := a.client.Query(ctx,
		`mutation($id: String!, $name: String!, $desc: String!, $cfg: JSON) {
			hub { agent { db { insert_agent_types(
				data: { id: $id, name: $name, description: $desc, config: $cfg }
			) { id } } } }
		}`,
		map[string]any{"id": id, "name": name, "desc": description, "cfg": config},
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
	a.logger.Info("agent type seeded", "id", id, "image", defaultAgentImage)
}
