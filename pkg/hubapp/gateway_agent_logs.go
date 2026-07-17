package hubapp

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

// agentLogsHandler serves GET /api/v1/agents/{id}/logs?tail=N — the tail of the
// agent container's combined stdout+stderr, as plain text. Admin only; drives
// the console's per-agent Logs view. The agent runtime (DockerRuntime) reads the
// container log directly, so this works even when the agent's HTTP API is down.
func (a *HubApp) agentLogsHandler(w http.ResponseWriter, r *http.Request) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	if err := a.requireAdmin(withIdentity(r.Context(), u), u); err != nil {
		gatewayError(w, http.StatusForbidden, "forbidden", "admin only")
		return
	}
	if a.agentRuntime == nil {
		gatewayError(w, http.StatusNotImplemented, "no_runtime", "agent runtime unavailable")
		return
	}

	agentID := r.PathValue("id")
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			tail = n
		}
	}
	if tail > 5000 {
		tail = 5000
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	logs, err := a.agentRuntime.Logs(ctx, agentID, tail)
	if err != nil {
		a.logger.Warn("agent logs", "agent", agentID, "error", err)
		gatewayError(w, http.StatusBadGateway, "logs_unavailable", "could not read agent logs")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, logs)
}
