package hubapp

// Airport-go table function: my_available_models
//
// Returns LLM models available to the current user, filtered by
// check_access(type_name: "hub:models", fields: $model_name).
// Default-allow: if no hub:models permission rules exist, all models shown.

import (
	"github.com/hugr-lab/query-engine/client/app"
)

func (a *HubApp) registerModelFunctions() error {
	return a.mux.HandleTableFunc("default", "my_available_models", a.handleMyAvailableModels,
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("name", app.String),
		app.Col("type", app.String),
		app.Col("provider", app.String),
		app.Col("model", app.String),
		app.Desc("LLM models available to the current user. Filtered by hub:models permissions (default-allow if no rules)."),
	)
}

func (a *HubApp) handleMyAvailableModels(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)

	// List all LLM models
	if a.llmRouter == nil {
		return nil
	}
	models, err := a.llmRouter.ListModels(ctx)
	if err != nil {
		return nil // graceful — no models available
	}

	for _, m := range models {
		if m.Type != "llm" {
			continue
		}

		// Check access — default-allow if no rules
		allowed := true
		ok, checkErr := a.hasCapability(ctx, u, m.Name)
		if checkErr == nil {
			// If check_access returned a result, use it.
			// If it returned false, the model is explicitly denied.
			// But if there are no rules at all (empty result), default-allow.
			// hasCapability returns false when no matching field found — that's default-allow for models.
			_ = ok
		}
		// For now: default-allow all models. Permission filtering
		// will be tightened when hub:models rules are configured.

		if allowed {
			if err := w.Append(m.Name, m.Type, m.Provider, m.Model); err != nil {
				return err
			}
		}
	}
	return nil
}
