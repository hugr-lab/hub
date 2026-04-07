package hubapp

import "github.com/hugr-lab/query-engine/client/app"

// registerCatalog sets up table functions in the CatalogMux.
// These become queryable via Hugr GraphQL as hub.memory_search(...), hub.registry_search(...).
func (a *HubApp) registerCatalog() error {
	// memory_search(query, user_id, limit) — semantic search over agent_memory
	err := a.mux.HandleTableFunc("default", "memory_search", func(w *app.Result, r *app.Request) error {
		// TODO: implement via Hugr GraphQL query with semantic search
		// For now return empty
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

	// registry_search(query, limit) — search saved queries
	err = a.mux.HandleTableFunc("default", "registry_search", func(w *app.Result, r *app.Request) error {
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
	if err != nil {
		return err
	}

	return nil
}
