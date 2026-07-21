package hubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Per-agent MCP tool providers — hub-native endpoints (like the skills panel).
// The console lists / adds / removes REMOTE HTTP MCP providers on an agent. The
// hub authorizes, persists the FULL desired provider list into the agent's
// config_override (the shallow top-level merge replaces the whole tool_providers
// array, so stdio + http are written together), then POSTs the agent's hugen
// reload so the change goes live in every session at once (per_agent providers
// on the root ToolManager).
//
//	GET    /api/v1/agents/{id}/tool-providers        → list (member+; forwarded)
//	POST   /api/v1/agents/{id}/tool-providers        → upsert a provider (owner)
//	DELETE /api/v1/agents/{id}/tool-providers/{name}  → remove a provider (owner)

const maxToolProviderBody = 64 << 10

// toolProviderInput is the console's create/update payload (a remote MCP spec).
type toolProviderInput struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // http | streamable-http | sse
	Endpoint  string            `json:"endpoint"`
	Headers   map[string]string `json:"headers,omitempty"`
	Auth      string            `json:"auth,omitempty"`
}

// agentToolProvidersListHandler forwards GET to the agent's hugen list (member+).
func (a *HubApp) agentToolProvidersListHandler(w http.ResponseWriter, r *http.Request) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	agentID := r.PathValue("id")
	if err := a.authorizeAgent(r.Context(), u, agentID, accessMember); err != nil {
		code, msg := agentDenyEnvelope(accessMember, agentID)
		gatewayError(w, http.StatusForbidden, code, msg)
		return
	}
	base, ok := a.resolveAgentBase(w, r, agentID)
	if !ok {
		return
	}
	a.proxyToAgent(w, r, agentID, base, "/v1/tool-providers")
}

func (a *HubApp) agentToolProviderUpsertHandler(w http.ResponseWriter, r *http.Request) {
	a.mutateToolProvider(w, r, false)
}

func (a *HubApp) agentToolProviderDeleteHandler(w http.ResponseWriter, r *http.Request) {
	a.mutateToolProvider(w, r, true)
}

// mutateToolProvider upserts (del=false) or removes (del=true) a remote MCP
// provider: owner-gated, persists the full desired list into config_override,
// then pushes a live reload.
func (a *HubApp) mutateToolProvider(w http.ResponseWriter, r *http.Request, del bool) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	agentID := r.PathValue("id")
	if err := a.authorizeAgent(r.Context(), u, agentID, accessOwner); err != nil {
		code, msg := agentDenyEnvelope(accessOwner, agentID)
		gatewayError(w, http.StatusForbidden, code, msg)
		return
	}

	var name string
	var spec map[string]any
	if del {
		name = r.PathValue("name")
		if name == "" {
			gatewayError(w, http.StatusBadRequest, "bad_request", "provider name required")
			return
		}
	} else {
		var in toolProviderInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxToolProviderBody)).Decode(&in); err != nil {
			gatewayError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || strings.TrimSpace(in.Endpoint) == "" {
			gatewayError(w, http.StatusBadRequest, "bad_request", "name and endpoint are required")
			return
		}
		in.Transport = strings.ToLower(strings.TrimSpace(in.Transport))
		if in.Transport == "" {
			in.Transport = "http"
		}
		if !isHTTPTransportLabel(in.Transport) {
			gatewayError(w, http.StatusBadRequest, "bad_request", "transport must be http, streamable-http, or sse")
			return
		}
		name = in.Name
		spec = map[string]any{
			"name": in.Name, "type": "mcp", "transport": in.Transport,
			"endpoint": in.Endpoint, "lifetime": "per_agent",
		}
		if len(in.Headers) > 0 {
			spec["headers"] = in.Headers
		}
		if in.Auth != "" {
			spec["auth"] = in.Auth
		}
	}

	rawOverride, mergedTP, err := a.readAgentToolProviders(r.Context(), agentID)
	if err != nil {
		a.logger.Info("tool-provider read failed", "agent", agentID, "error", err)
		gatewayError(w, http.StatusBadGateway, "read_failed", "could not read agent config")
		return
	}

	// Guard: an upsert must not hijack the name of a built-in (stdio / non-HTTP)
	// provider — editProviderList replaces by name, so this would silently swap
	// out e.g. bash-mcp for a remote endpoint.
	if !del {
		for _, p := range mergedTP {
			if providerName(p) == name && !isHTTPProviderEntry(p) {
				gatewayError(w, http.StatusConflict, "name_conflict", "a built-in provider named "+name+" already exists")
				return
			}
		}
	}

	// Apply the edit to the FULL provider list (stdio + http together).
	next, found := editProviderList(mergedTP, name, spec, del)
	if del && !found {
		gatewayError(w, http.StatusNotFound, "not_found", "no provider named "+name)
		return
	}

	if rawOverride == nil {
		rawOverride = map[string]any{}
	}
	rawOverride["tool_providers"] = next
	if err := a.updateAgentIdentity(r.Context(), agentID, map[string]any{"config_override": rawOverride}); err != nil {
		a.logger.Warn("tool-provider persist failed", "agent", agentID, "error", err)
		gatewayError(w, http.StatusBadGateway, "persist_failed", "could not save config")
		return
	}

	// Push the reload so it goes live now (best-effort; the config is persisted
	// regardless — a restart or the next reload will pick it up).
	applied := a.pushToolProviderReload(r.Context(), agentID, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "persisted": true, "applied": applied})
}

// readAgentToolProviders returns the agent's raw config_override plus the merged
// tool_providers list (config_override's array wins over the type's — the shallow
// top-level merge semantics).
func (a *HubApp) readAgentToolProviders(ctx context.Context, agentID string) (map[string]any, []any, error) {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { agent { db { agents(
			filter: { id: { eq: $id } } limit: 1
		) { config_override agent_type { config } } } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("read agent: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, nil, fmt.Errorf("read agent: %w", res.Err())
	}
	var rows []struct {
		ConfigOverride map[string]any `json:"config_override"`
		AgentType      struct {
			Config map[string]any `json:"config"`
		} `json:"agent_type"`
	}
	if err := res.ScanData("hub.agent.db.agents", &rows); err != nil {
		return nil, nil, fmt.Errorf("read agent: scan: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("agent %q not found", agentID)
	}
	override := rows[0].ConfigOverride
	tp := rows[0].AgentType.Config["tool_providers"]
	if ov, ok := override["tool_providers"]; ok {
		tp = ov // config_override replaces the whole array
	}
	list, _ := tp.([]any)
	return override, list, nil
}

// pushToolProviderReload POSTs the agent's hugen reload endpoint, forwarding the
// user bearer. Best-effort — reports whether it applied.
func (a *HubApp) pushToolProviderReload(ctx context.Context, agentID, bearer string) bool {
	if a.agentRuntime == nil {
		return false
	}
	base, err := a.agentRuntime.APIBaseURL(agentID)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/tool-providers/reload", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", bearer)
	resp, err := gatewayClient().Do(req)
	if err != nil {
		a.logger.Warn("tool-provider reload push failed", "agent", agentID, "error", err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// editProviderList applies one edit to the full provider list, preserving every
// other entry (stdio providers included). del removes `name`; otherwise `spec`
// replaces an existing entry of that name or is appended. found reports whether
// an entry of that name was present.
func editProviderList(list []any, name string, spec map[string]any, del bool) (next []any, found bool) {
	next = make([]any, 0, len(list)+1)
	for _, p := range list {
		if providerName(p) == name {
			found = true
			if del {
				continue // drop it
			}
			next = append(next, spec) // replace
			continue
		}
		next = append(next, p)
	}
	if !del && !found {
		next = append(next, spec) // add new
	}
	return next, found
}

// isHTTPProviderEntry reports whether a tool_providers entry is a remote
// HTTP/SSE MCP (vs a stdio / built-in provider).
func isHTTPProviderEntry(p any) bool {
	if m, ok := p.(map[string]any); ok {
		if t, ok := m["transport"].(string); ok {
			return isHTTPTransportLabel(strings.ToLower(t))
		}
	}
	return false
}

// providerName extracts the "name" field of a tool_providers entry.
func providerName(p any) string {
	if m, ok := p.(map[string]any); ok {
		if n, ok := m["name"].(string); ok {
			return n
		}
	}
	return ""
}

// isHTTPTransportLabel accepts the remote MCP transports (matches hugen's
// tool.IsHTTPTransport).
func isHTTPTransportLabel(t string) bool {
	switch t {
	case "http", "streamable-http", "sse":
		return true
	}
	return false
}
