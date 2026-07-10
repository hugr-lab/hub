package hubapp

// Agent provisioning CRUD (spec-hub-side HB2) — admin-only creation, mutation,
// and disabling of agent identities in the Agent DB (hub.agent.db.agents, the
// canon). Distinct from the runtime lifecycle mutations (start/stop/delete in
// handlers_agent.go, which drive the container): these own the DB identity.
//
//   create_agent   insert identity + optional owner grant + mint bootstrap → boot
//   update_agent   admin edit of name / role / status / config_override (only exit from 'disabled')
//   disable_agent  admin revoke: status='disabled' (token gate denies within one TTL) + stop
//
// Agents are NEVER self-created — agent_info stays resolve-only (a container
// minting its own identity is privilege escalation). Provisioning is an admin
// act: the role (D10, admin-assigned) and the JWT it stamps are security-
// load-bearing, so create_agent is gated on hub:management.admin like the other
// lifecycle functions.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hugr-lab/query-engine/client/app"
)

// agentIDPattern constrains a caller-supplied agent id: it becomes the container
// name, the agent-network DNS host, and a /data bind path segment, so it must be
// a safe DNS label with no path-traversal characters (spec-agent-orchestration
// §3). 41 chars max keeps hub-agent-<id> within Docker's name limits.
const agentIDPattern = "^[a-z0-9][a-z0-9-]{0,40}$"

var agentIDRe = regexp.MustCompile(agentIDPattern)

// validAgentStatus reports whether s is a valid agents.status desired state
// (spec §4): active / manual / paused (owner run-state) or disabled (admin
// revocation). 'manual' is hands-off — the supervisor never auto-starts it; it
// runs only via an explicit start_agent.
func validAgentStatus(s string) bool {
	switch s {
	case "active", "manual", "paused", "disabled":
		return true
	}
	return false
}

// agentProvisionType is what create_agent returns: the new identity plus a
// one-shot bootstrap secret for its first /agent/token exchange. The secret is
// returned ONCE — only its hash is stored. `secret`/`expires_at` are empty when
// the token issuer is disabled (identity is still created; mint later).
func agentProvisionType() app.Type {
	return app.Struct("agent_provision").
		Desc("A newly provisioned agent identity plus its one-shot bootstrap secret (returned once; only the hash is stored). secret/expires_at are empty when the token issuer is disabled.").
		Field("agent_id", app.String).
		Field("status", app.String).
		Field("secret", app.String).
		Field("expires_at", app.String).
		AsType()
}

// agentIdentityType is the compact identity view update_agent / disable_agent
// return so the admin UI can patch its row without a re-fetch.
func agentIdentityType() app.Type {
	return app.Struct("agent_identity").
		Desc("Agent identity after a provisioning mutation.").
		Field("id", app.String).
		Field("name", app.String).
		Field("role", app.String).
		Field("status", app.String).
		AsType()
}

// registerProvisioningMutations wires the admin identity-CRUD functions into
// the `default` module, next to start/stop/delete_agent.
func (a *HubApp) registerProvisioningMutations() error {
	// NOTE: the agent's assigned role is the arg `hugr_role`, NOT `role` — `role`
	// is a reserved hidden ArgFromContext (the CALLER's auth role); declaring an
	// input named `role` collides with it at registration.
	//
	// The app framework generates every non-context arg as non-null (String! /
	// JSON!) — there is no optional-arg support. So hugr_role / owner_user_id /
	// config_override are REQUIRED on the wire; the handlers treat an empty
	// string / empty {} as "use the default / skip / leave unchanged".
	if err := a.mux.HandleFunc("default", "create_agent", a.handleCreateAgent,
		app.Arg("agent_id", app.String),
		app.Arg("agent_type_id", app.String),
		app.Arg("name", app.String),
		app.Arg("hugr_role", app.String),
		app.Arg("owner_user_id", app.String),
		app.Arg("config_override", app.JSON),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentProvisionType()),
		app.Mutation(),
		app.Desc("Provision a new agent identity in the Agent DB and mint its one-shot bootstrap secret (spec-hub-side HB2). agent_type_id must exist. All args are required by the framework: pass hugr_role='' for the default 'agent' role (admin-assigned, D10, stamped into the JWT), owner_user_id='' to skip the owner grant, config_override='{}' for no per-instance overrides. Returns {agent_id, status, secret, expires_at}. Admin only."),
	); err != nil {
		return err
	}

	if err := a.mux.HandleFunc("default", "update_agent", a.handleUpdateAgent,
		app.Arg("agent_id", app.String),
		app.Arg("name", app.String),
		app.Arg("hugr_role", app.String),
		app.Arg("status", app.String),
		app.Arg("config_override", app.JSON),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentIdentityType()),
		app.Mutation(),
		app.Desc("Edit an agent identity in the Agent DB. All args are required by the framework; an empty string / empty {} leaves that field unchanged, so pass only what you want to change (name / hugr_role / status / config_override). status ∈ {active, manual, paused, disabled}; setting 'manual' STOPS a running container (manual baseline — start_agent relaunches it, restart-policy 'no'). hugr_role changes are privilege-sensitive. Returns the resulting identity. Admin only."),
	); err != nil {
		return err
	}

	if err := a.mux.HandleFunc("default", "disable_agent", a.handleDisableAgent,
		app.Arg("agent_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentIdentityType()),
		app.Mutation(),
		app.Desc("Revoke an agent (admin): set status='disabled' (its next /agent/token refresh is denied within one token TTL) and stop its container. An owner cannot reverse this — only update_agent status='active' can. Admin only."),
	); err != nil {
		return err
	}

	return nil
}

// handleCreateAgent provisions a new agent identity in the Agent DB, optionally
// grants an owner, and mints a bootstrap secret for its first token exchange.
func (a *HubApp) handleCreateAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}

	id := strings.TrimSpace(r.String("agent_id"))
	typeID := strings.TrimSpace(r.String("agent_type_id"))
	name := strings.TrimSpace(r.String("name"))
	role := strings.TrimSpace(r.String("hugr_role")) // the AGENT's role (arg is hugr_role, not role — see registration note)
	ownerUserID := strings.TrimSpace(r.String("owner_user_id"))
	if id == "" {
		return errors.New("agent_id is required")
	}
	// The id becomes the container name (hub-agent-<id>), its DNS host on the
	// agent network, AND a path segment of the /data bind — so it must be a safe
	// DNS label with no path-traversal characters (spec-agent-orchestration §3).
	if !agentIDRe.MatchString(id) {
		return fmt.Errorf("agent_id %q is invalid: must match %s (lowercase alphanumerics + hyphens, 1-41 chars, no leading hyphen)", id, agentIDPattern)
	}
	if typeID == "" {
		return errors.New("agent_type_id is required")
	}
	if name == "" {
		return errors.New("name is required")
	}
	if role == "" {
		role = "agent" // D10 default; admin overrides deliberately
	}
	var configOverride map[string]any
	if err := r.JSON("config_override", &configOverride); err != nil {
		return fmt.Errorf("config_override is not valid JSON: %w", err)
	}
	if len(configOverride) == 0 {
		configOverride = nil // empty {} sentinel → store SQL NULL, not an empty object
	}

	// The agent id is the hugr principal (user_id == agent_id, D8) — it must be
	// free. agentForToken returns errAgentNotRegistered when the row is absent,
	// which is exactly the free-to-create case.
	switch _, err := a.agentForToken(ctx, id); {
	case err == nil:
		return fmt.Errorf("agent %q already exists", id)
	case errors.Is(err, errAgentNotRegistered):
		// free — proceed
	default:
		return fmt.Errorf("check agent existence: %w", err)
	}

	// The agent_type must exist in the Agent DB (FK agents.agent_type_id →
	// agent_types.id); catch it here for a clear message rather than a raw FK error.
	if err := a.agentTypeExists(ctx, typeID); err != nil {
		return err
	}

	if err := a.insertAgentIdentity(ctx, id, typeID, shortIDFromAgentID(id), name, role, configOverride); err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// Optional owner grant (platform DB). Best-effort: a failed grant must not
	// leave the caller without the bootstrap secret it needs to boot — the grant
	// can be re-added, an un-returned secret cannot.
	if ownerUserID != "" {
		a.grantAgentOwner(ctx, ownerUserID, id)
	}

	// Provision-and-boot: hand back a bootstrap secret so the caller can start
	// the agent immediately. If the issuer is disabled the identity still exists;
	// the secret is minted later via bootstrap_token once the issuer is on.
	var secret, expiresAt string
	if a.config.AgentJWTKeyFile != "" {
		s, exp, err := a.mintBootstrapForAgent(ctx, id)
		if err != nil {
			return fmt.Errorf("agent %q created but bootstrap mint failed: %w", id, err)
		}
		secret = s
		expiresAt = exp.Format(time.RFC3339Nano)
	} else {
		a.logger.Warn("agent created without bootstrap secret (token issuer disabled)", "agent", id)
	}

	a.logger.Info("agent provisioned", "agent", id, "type", typeID, "role", role, "by", u.ID)
	return w.SetJSON(map[string]any{
		"agent_id":   id,
		"status":     "active",
		"secret":     secret,
		"expires_at": expiresAt,
	})
}

// handleUpdateAgent edits an existing agent identity — partial: only the
// provided fields change.
func (a *HubApp) handleUpdateAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}

	agentID := strings.TrimSpace(r.String("agent_id"))
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	before, err := a.agentForToken(ctx, agentID)
	if err != nil {
		return err // errAgentNotRegistered → clear "no such agent"
	}

	data := map[string]any{}
	if v := strings.TrimSpace(r.String("name")); v != "" {
		data["name"] = v
	}
	if v := strings.TrimSpace(r.String("hugr_role")); v != "" { // arg is hugr_role; column is role
		data["role"] = v
	}
	if v := strings.TrimSpace(r.String("status")); v != "" {
		// update_agent is the only free-form status writer and the ONLY exit from
		// 'disabled' (spec §4) — reject typos that would brick an agent (the
		// supervisor treats an unknown desired status as a no-op).
		if !validAgentStatus(v) {
			return fmt.Errorf("invalid status %q: must be one of active, manual, paused, disabled", v)
		}
		data["status"] = v
	}
	var configOverride map[string]any
	if err := r.JSON("config_override", &configOverride); err != nil {
		return fmt.Errorf("config_override is not valid JSON: %w", err)
	}
	// A non-empty override replaces the stored one; an empty {} means "leave
	// unchanged" (args are always required in this framework, so {} is the
	// omit-this-field sentinel — a partial update must not wipe config_override).
	if len(configOverride) > 0 {
		data["config_override"] = configOverride
	}
	if len(data) == 0 {
		return errors.New("nothing to update: provide at least one of name / hugr_role / status / config_override (empty string / {} leaves a field unchanged)")
	}

	if err := a.updateAgentIdentity(ctx, agentID, data); err != nil {
		return fmt.Errorf("update agent: %w", err)
	}

	// Transition INTO 'manual' brings the agent to its manual baseline — STOP any
	// running container. The supervisor is hands-off for manual (decide=actNone),
	// so it would otherwise leave a container that was created while 'active' still
	// running with restart-policy 'unless-stopped' — a crash would auto-revive,
	// defeating the manual contract. start_agent relaunches it (restart-policy
	// 'no') when the operator wants it up. Only on a real transition (was ≠ manual).
	if s, _ := data["status"].(string); s == "manual" && before.Status != "manual" {
		if a.supervisor != nil {
			_ = a.supervisor.stopManual(r.Context(), agentID)
		} else if a.dockerRuntime != nil {
			_ = a.dockerRuntime.Stop(r.Context(), agentID)
		}
	}

	info, err := a.agentForToken(ctx, agentID)
	if err != nil {
		return fmt.Errorf("update agent: re-read: %w", err)
	}
	a.logger.Info("agent updated", "agent", agentID, "fields", len(data), "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":     info.ID,
		"name":   info.Name,
		"role":   info.Role,
		"status": info.Status,
	})
}

// handleDisableAgent is the ADMIN revocation: it flips status to 'disabled' (the
// next /agent/token refresh is denied within one token TTL) and converges the
// container down. Unlike owner stop_agent (→ 'paused'), only update_agent (admin)
// can leave 'disabled' (spec §4), so an owner cannot un-revoke.
func (a *HubApp) handleDisableAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}

	agentID := strings.TrimSpace(r.String("agent_id"))
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	info, err := a.agentForToken(ctx, agentID)
	if err != nil {
		return err
	}

	svcCtx := r.Context()
	if err := a.updateAgentIdentity(svcCtx, agentID, map[string]any{"status": "disabled"}); err != nil {
		return fmt.Errorf("disable agent: %w", err)
	}

	// Stop the container now (its next token refresh is already denied by the
	// status gate; stopping frees resources immediately). Idempotent.
	if a.supervisor != nil {
		a.supervisor.kick(svcCtx, agentID, "disabled")
	} else if a.dockerRuntime != nil {
		_ = a.dockerRuntime.Stop(svcCtx, agentID)
	}

	a.logger.Info("agent disabled", "agent", agentID, "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":     info.ID,
		"name":   info.Name,
		"role":   info.Role,
		"status": "disabled",
	})
}

// ---- Agent DB helpers ----

// agentTypeExists returns nil iff the agent_type is present in the Agent DB.
func (a *HubApp) agentTypeExists(ctx context.Context, typeID string) error {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { agent { db { agent_types(
			filter: { id: { eq: $id } } limit: 1
		) { id } } } } }`,
		map[string]any{"id": typeID},
	)
	if err != nil {
		return fmt.Errorf("check agent type %q: %w", typeID, err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("check agent type %q: %w", typeID, res.Err())
	}
	var types []struct {
		ID string `json:"id"`
	}
	if err := res.ScanData("hub.agent.db.agent_types", &types); err != nil || len(types) == 0 {
		return fmt.Errorf("agent_type %q not found in the Agent DB", typeID)
	}
	return nil
}

// insertAgentIdentity writes the agents row into the Agent DB. config_override
// is passed as a JSON variable so a nil override lands as SQL NULL.
func (a *HubApp) insertAgentIdentity(ctx context.Context, id, typeID, shortID, name, role string, configOverride map[string]any) error {
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $tid: String!, $sid: String!, $name: String!, $role: String!, $cfg: JSON) {
			hub { agent { db { insert_agents(data: {
				id: $id, agent_type_id: $tid, short_id: $sid, name: $name, status: "active", role: $role, config_override: $cfg
			}) { id } } } } }`,
		map[string]any{"id": id, "tid": typeID, "sid": shortID, "name": name, "role": role, "cfg": configOverride},
	)
	if err != nil {
		return err
	}
	defer res.Close()
	return res.Err()
}

// updateAgentIdentity applies a partial update to the agents row in the Agent
// DB. affected_rows is not checked — hugr's postgres source reports 0 even on a
// successful update (known query-engine quirk); the caller verifies existence
// up front and re-reads for the response.
func (a *HubApp) updateAgentIdentity(ctx context.Context, agentID string, data map[string]any) error {
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $data: hub_agent_db_agents_mut_data!) {
			hub { agent { db { update_agents(
				filter: { id: { eq: $id } } data: $data
			) { affected_rows } } } } }`,
		map[string]any{"id": agentID, "data": data},
	)
	if err != nil {
		return err
	}
	defer res.Close()
	return res.Err()
}

// grantAgentOwner ensures the user row exists (FK) then writes an owner grant in
// the platform DB. Best-effort: logged, never fatal to provisioning.
func (a *HubApp) grantAgentOwner(ctx context.Context, userID, agentID string) {
	a.ensureUser(ctx, userID, "")
	res, err := a.client.Query(ctx,
		`mutation($uid: String!, $aid: String!) {
			hub { db { insert_user_agents(data: {
				user_id: $uid, agent_id: $aid, role: "owner"
			}) { user_id } } } }`,
		map[string]any{"uid": userID, "aid": agentID},
	)
	if err != nil {
		a.logger.Warn("grant agent owner failed", "agent", agentID, "user", userID, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		a.logger.Warn("grant agent owner error", "agent", agentID, "user", userID, "error", res.Err())
	}
}

// shortIDFromAgentID derives the NOT-NULL short_id alias from a caller-supplied
// agent id: the first four alphanumerics (lower-cased). When the id has fewer
// than four (e.g. a UUID starting with dashes, or a very short id), it is padded
// with random hex so the column is always satisfied.
func shortIDFromAgentID(id string) string {
	b := make([]byte, 0, 4)
	for i := 0; i < len(id) && len(b) < 4; i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b = append(b, c)
		case c >= 'A' && c <= 'Z':
			b = append(b, c+('a'-'A'))
		}
	}
	if len(b) < 4 {
		pad := make([]byte, 4)
		_, _ = rand.Read(pad)
		b = append(b, []byte(hex.EncodeToString(pad))...)
	}
	return string(b[:4])
}
