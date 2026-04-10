package hubapp

import (
	"time"

	"github.com/hugr-lab/query-engine/client/app"
)

// registerCatalog registers all airport-go scalar/table/mutating functions in the CatalogMux.
// Read functions land under `function { hub { ... } }` (or, for table functions, `hub { ... }`),
// mutating ones under `mutation { function { hub { ... } } }`.
func (a *HubApp) registerCatalog() error {
	if err := a.registerSearchFunctions(); err != nil {
		return err
	}
	if err := a.registerAgentRuntime(); err != nil {
		return err
	}
	if err := a.registerReadFunctions(); err != nil {
		return err
	}
	if err := a.registerAgentMutations(); err != nil {
		return err
	}
	if err := a.registerConversationMutations(); err != nil {
		return err
	}
	return nil
}

// registerSearchFunctions registers read-only search table functions.
func (a *HubApp) registerSearchFunctions() error {
	err := a.mux.HandleTableFunc("default", "memory_search", func(w *app.Result, r *app.Request) error {
		// TODO: implement via Hugr GraphQL query with semantic search
		return nil
	},
		app.Arg("query", app.String),
		app.Arg("user_id", app.String),
		app.Arg("limit", app.Int64),
		app.ColPK("id", app.String),
		app.Col("content", app.String),
		app.Col("category", app.String),
		app.Col("distance", app.Float64),
		app.Desc("Semantic search over agent memory"),
	)
	if err != nil {
		return err
	}

	return a.mux.HandleTableFunc("default", "registry_search", func(w *app.Result, r *app.Request) error {
		// TODO: implement via Hugr GraphQL query
		return nil
	},
		app.Arg("query", app.String),
		app.Arg("limit", app.Int64),
		app.ColPK("id", app.String),
		app.Col("name", app.String),
		app.Col("query_text", app.String),
		app.Col("description", app.String),
		app.Col("usage_count", app.Int64),
		app.Desc("Search saved queries in registry"),
	)
}

// registerAgentRuntime registers the read-only agent_runtime table function
// that exposes in-memory state of running agents.
func (a *HubApp) registerAgentRuntime() error {
	return a.mux.HandleTableFunc("default", "agent_runtime", func(w *app.Result, r *app.Request) error {
		if a.dockerRuntime == nil {
			return nil
		}
		filterAgentID := r.String("agent_id")
		for _, state := range a.dockerRuntime.ListRunning() {
			if filterAgentID != "" && state.AgentID != filterAgentID {
				continue
			}
			containerShort := state.ContainerID
			if len(containerShort) > 12 {
				containerShort = containerShort[:12]
			}
			if err := w.Append(
				state.AgentID,
				state.DisplayName,
				state.AgentTypeID,
				containerShort,
				state.Status,
				state.StartedAt.Format(time.RFC3339),
			); err != nil {
				return err
			}
		}
		return nil
	},
		app.Arg("agent_id", app.String),
		app.ColPK("agent_id", app.String),
		app.Col("display_name", app.String),
		app.Col("agent_type_id", app.String),
		app.Col("container_id", app.String),
		app.Col("status", app.String),
		app.Col("started_at", app.String),
		app.Desc("Live runtime state of agents from Hub Service memory. Pass empty agent_id to list all running agents, or specific id to filter."),
	)
}
