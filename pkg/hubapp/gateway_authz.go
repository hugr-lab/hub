package hubapp

import (
	"context"

	"github.com/hugr-lab/hub/pkg/auth"
)

// agentAccessLevel is the grant a caller needs for an agent-scoped operation.
// One policy, one dispatcher — every /api/v1/agents/{id}/* and chat-transport
// route states its level and calls authorizeAgent, instead of each handler
// hand-picking checkAccess / checkOwner / requireAdmin. The operation→grant
// matrix therefore lives in exactly one visible place.
//
// The whole model rests on a NETWORK guarantee: an agent container's HTTP API
// (10200/tcp) is reachable ONLY from inside the agent network — the hub is the
// only door (agentmgr publishes a host port solely under the dev-only
// HUB_AGENT_PUBLISH_API). So the hub is the single point where agent-access is
// authorized; hugen downstream authorizes only resource ownership (its own
// sessions / tasks / artifacts) and trusts that every request came through here.
//
// Policy:
//
//	member — reach the agent at all: the generic /hugen/ session API, chat
//	         verbs, skills LIST. hugen then enforces per-session ownership.
//	owner  — privileged management that stays within the agent: skills EXPORT
//	         + INSTALL (mutating / bundle-exfil).
//	admin  — operations that cross user/agent boundaries: container LOGS (raw
//	         stdout may carry every user's activity on a shared agent), and the
//	         lifecycle/grant GraphQL mutations (create/start/stop/delete/grant —
//	         those live on the GraphQL plane and call requireAdmin/checkOwner
//	         directly; listed here so the whole picture is in one comment).
type agentAccessLevel int

const (
	accessMember agentAccessLevel = iota
	accessOwner
	accessAdmin
)

// authorizeAgent applies the agent-access policy for level. It dispatches to the
// seam-bearing primitives — checkAccess / checkOwner honor the accessCheck /
// ownerCheck test overrides, requireAdmin is the capability gate — so behavior,
// including the management-auth and admin-capability bypasses, is identical to
// the direct calls it replaces. checkAgentAccess reads hub.db.user_agents (the
// grant) as the service principal and evaluates the admin capability under the
// caller's own role (hasCapability → withIdentity); passing r.Context() is
// correct for every level.
func (a *HubApp) authorizeAgent(ctx context.Context, u auth.UserInfo, agentID string, level agentAccessLevel) error {
	switch level {
	case accessAdmin:
		return a.requireAdmin(ctx, u)
	case accessOwner:
		return a.checkOwner(ctx, u, agentID)
	default:
		return a.checkAccess(ctx, u, agentID)
	}
}

// agentDenyEnvelope maps a denied level to the gateway error code + message.
func agentDenyEnvelope(level agentAccessLevel, agentID string) (code, msg string) {
	switch level {
	case accessAdmin:
		return "forbidden", "admin access required"
	case accessOwner:
		return "owner_required", "owner or admin access required"
	default:
		return "no_agent_access", "no access to agent " + agentID
	}
}
