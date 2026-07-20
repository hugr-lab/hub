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
	a.forwardAgentSkills(w, r, false, "/v1/skills")
}

func (a *HubApp) agentSkillExportHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		gatewayError(w, http.StatusBadRequest, "bad_request", "skill name required")
		return
	}
	a.forwardAgentSkills(w, r, true, "/v1/skills/"+url.PathEscape(name)+"/export")
}

func (a *HubApp) agentSkillInstallHandler(w http.ResponseWriter, r *http.Request) {
	a.forwardAgentSkills(w, r, true, "/v1/skills/install")
}

// forwardAgentSkills authorizes the caller — owner/admin for privileged ops
// (export / install), member+ for reads (list) — then reverse-proxies to the
// agent's hugen skills API at rest, forwarding the user bearer and the request
// query (e.g. ?overwrite=true on install).
func (a *HubApp) forwardAgentSkills(w http.ResponseWriter, r *http.Request, privileged bool, rest string) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	agentID := r.PathValue("id")

	authz := a.checkAccess
	if privileged {
		authz = a.checkOwner
	}
	if err := authz(r.Context(), u, agentID); err != nil {
		code, msg := "no_agent_access", "no access to agent "+agentID
		if privileged {
			code, msg = "owner_required", "installing or exporting a skill requires owner or admin access"
		}
		a.logger.Info("agent skills op denied", "agent", agentID, "user", u.ID, "privileged", privileged, "error", err)
		gatewayError(w, http.StatusForbidden, code, msg)
		return
	}

	base, ok := a.resolveAgentBase(w, r, agentID)
	if !ok {
		return
	}
	a.proxyToAgent(w, r, agentID, base, rest)
}
