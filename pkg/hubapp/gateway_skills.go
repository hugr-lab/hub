package hubapp

import (
	"net/http"
	"net/url"
)

// The per-agent skills surface — hub-native endpoints, consistent with the chat
// (/api/v1/chats/…) and logs (/api/v1/agents/{id}/logs) surfaces. The console
// reads / exports / installs an agent's skills through the hub, which authorizes
// then reverse-proxies to the agent's hugen /v1/skills API. This keeps the
// console off the generic /hugen/ passthrough proxy.
//
//	GET  /api/v1/agents/{id}/skills                → list installed (member+)
//	GET  /api/v1/agents/{id}/skills/{name}/export  → bundle tar.gz (owner/admin)
//	POST /api/v1/agents/{id}/skills/install        → install a bundle (owner/admin)

func (a *HubApp) agentSkillsListHandler(w http.ResponseWriter, r *http.Request) {
	a.forwardAgentSkills(w, r, accessMember, "/v1/skills")
}

func (a *HubApp) agentSkillExportHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		gatewayError(w, http.StatusBadRequest, "bad_request", "skill name required")
		return
	}
	a.forwardAgentSkills(w, r, accessOwner, "/v1/skills/"+url.PathEscape(name)+"/export")
}

func (a *HubApp) agentSkillInstallHandler(w http.ResponseWriter, r *http.Request) {
	a.forwardAgentSkills(w, r, accessOwner, "/v1/skills/install")
}

// forwardAgentSkills authorizes the caller at the policy level (member for list,
// owner for export/install — see gateway_authz.go), then reverse-proxies to the
// agent's hugen skills API at rest, forwarding the user bearer and the request
// query (e.g. ?overwrite=true on install).
func (a *HubApp) forwardAgentSkills(w http.ResponseWriter, r *http.Request, level agentAccessLevel, rest string) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	agentID := r.PathValue("id")

	if err := a.authorizeAgent(r.Context(), u, agentID, level); err != nil {
		code, msg := agentDenyEnvelope(level, agentID)
		a.logger.Info("agent skills op denied", "agent", agentID, "user", u.ID, "level", level, "error", err)
		gatewayError(w, http.StatusForbidden, code, msg)
		return
	}

	base, ok := a.resolveAgentBase(w, r, agentID)
	if !ok {
		return
	}
	a.proxyToAgent(w, r, agentID, base, rest)
}
