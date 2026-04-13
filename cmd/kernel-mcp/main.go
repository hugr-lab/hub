// kernel-mcp is a stdio MCP server for Jupyter kernel management.
// It starts, executes code in, lists, and stops kernel sessions
// via jupyter-server's REST API and WebSocket kernel channels.
//
// Only available in workspace context (context: local).
//
// Usage:
//
//	kernel-mcp                            # stdio mode
//	JUPYTER_SERVER_URL=http://localhost:8888
//	JUPYTER_TOKEN=<token>
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/coder/websocket"
)

var (
	jupyterURL   string
	jupyterToken string
	sessions     sync.Map // session_id → kernelSession
)

type kernelSession struct {
	ID        string    `json:"session_id"`
	Kind      string    `json:"kind"`
	KernelID  string    `json:"kernel_id"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used_at"`
}

func main() {
	jupyterURL = os.Getenv("JUPYTER_SERVER_URL")
	if jupyterURL == "" {
		jupyterURL = "http://localhost:8888"
	}
	jupyterToken = os.Getenv("JUPYTER_TOKEN")

	srv := server.NewMCPServer("kernel-mcp", "0.1.0",
		server.WithToolCapabilities(true),
	)

	srv.AddTool(mcp.NewTool("kernel.start_session",
		mcp.WithDescription("Start a new Jupyter kernel session."),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Kernel type: python, duckdb, or hugr")),
	), handleStartSession)

	srv.AddTool(mcp.NewTool("kernel.execute",
		mcp.WithDescription("Execute code in an existing kernel session. Returns stdout, stderr, and display data."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID from kernel.start_session")),
		mcp.WithString("code", mcp.Required(), mcp.Description("Code to execute")),
	), handleExecute)

	srv.AddTool(mcp.NewTool("kernel.list_sessions",
		mcp.WithDescription("List active kernel sessions."),
	), handleListSessions)

	srv.AddTool(mcp.NewTool("kernel.stop",
		mcp.WithDescription("Stop a kernel session."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to stop")),
	), handleStop)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	stdio := server.NewStdioServer(srv)
	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		log.Fatalf("kernel-mcp: %v", err)
	}
}

// kernelName maps our kind names to jupyter kernel spec names
func kernelName(kind string) string {
	switch kind {
	case "python":
		return "python3"
	case "duckdb":
		return "duckdb"
	case "hugr":
		return "hugr"
	default:
		return kind
	}
}

func handleStartSession(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	kind, _ := args["kind"].(string)
	if kind == "" {
		return toolError("kind is required (python, duckdb, hugr)"), nil
	}

	// POST /api/kernels to start a new kernel
	body, _ := json.Marshal(map[string]string{"name": kernelName(kind)})
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", jupyterURL+"/api/kernels", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if jupyterToken != "" {
		httpReq.Header.Set("Authorization", "token "+jupyterToken)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return toolError(fmt.Sprintf("start kernel: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return toolError(fmt.Sprintf("start kernel failed (%d): %s", resp.StatusCode, string(respBody))), nil
	}

	var kernel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&kernel)

	sessionID := fmt.Sprintf("k-%s", kernel.ID[:8])
	now := time.Now()
	sessions.Store(sessionID, kernelSession{
		ID:        sessionID,
		Kind:      kind,
		KernelID:  kernel.ID,
		CreatedAt: now,
		LastUsed:  now,
	})

	result, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"kind":       kind,
		"kernel_id":  kernel.ID,
		"created_at": now.Format(time.RFC3339),
	})
	return toolResult(string(result)), nil
}

func handleExecute(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sessionID, _ := args["session_id"].(string)
	code, _ := args["code"].(string)
	if sessionID == "" || code == "" {
		return toolError("session_id and code are required"), nil
	}

	val, ok := sessions.Load(sessionID)
	if !ok {
		return toolError(fmt.Sprintf("session %q not found — use kernel.start_session first", sessionID)), nil
	}
	sess := val.(kernelSession)

	// Connect to kernel via WebSocket channels
	wsURL := strings.Replace(jupyterURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/api/kernels/" + sess.KernelID + "/channels"
	if jupyterToken != "" {
		wsURL += "?token=" + jupyterToken
	}

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return toolError(fmt.Sprintf("connect to kernel: %v", err)), nil
	}
	defer conn.CloseNow()

	// Send execute_request on shell channel
	msgID := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	execMsg := map[string]any{
		"header": map[string]any{
			"msg_id":   msgID,
			"msg_type": "execute_request",
			"username": "hub-agent",
			"session":  sessionID,
			"version":  "5.3",
		},
		"parent_header": map[string]any{},
		"metadata":      map[string]any{},
		"content": map[string]any{
			"code":             code,
			"silent":           false,
			"store_history":    true,
			"allow_stdin":      false,
			"stop_on_error":    true,
		},
		"channel": "shell",
	}

	msgBytes, _ := json.Marshal(execMsg)
	if err := conn.Write(ctx, websocket.MessageText, msgBytes); err != nil {
		return toolError(fmt.Sprintf("send execute: %v", err)), nil
	}

	// Collect output from iopub channel
	var stdout, stderr strings.Builder
	var displayData any
	var status string
	var traceback []string
	execCount := 0

	deadline := time.After(120 * time.Second)
	for {
		select {
		case <-deadline:
			return toolError("execution timed out (120s)"), nil
		case <-ctx.Done():
			return toolError("cancelled"), nil
		default:
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}

		var msg struct {
			Channel      string         `json:"channel"`
			ParentHeader map[string]any `json:"parent_header"`
			Header       map[string]any `json:"header"`
			Content      map[string]any `json:"content"`
		}
		json.Unmarshal(data, &msg)

		// Only process messages for our execution
		parentMsgID, _ := msg.ParentHeader["msg_id"].(string)
		if parentMsgID != msgID {
			continue
		}

		msgType, _ := msg.Header["msg_type"].(string)
		switch msgType {
		case "stream":
			streamName, _ := msg.Content["name"].(string)
			text, _ := msg.Content["text"].(string)
			if streamName == "stderr" {
				stderr.WriteString(text)
			} else {
				stdout.WriteString(text)
			}
		case "execute_result":
			if data, ok := msg.Content["data"].(map[string]any); ok {
				if text, ok := data["text/plain"].(string); ok {
					stdout.WriteString(text)
				}
			}
			if ec, ok := msg.Content["execution_count"].(float64); ok {
				execCount = int(ec)
			}
		case "display_data":
			displayData = msg.Content["data"]
		case "error":
			status = "error"
			if tb, ok := msg.Content["traceback"].([]any); ok {
				for _, t := range tb {
					if s, ok := t.(string); ok {
						traceback = append(traceback, s)
					}
				}
			}
			if ename, ok := msg.Content["ename"].(string); ok {
				evalue, _ := msg.Content["evalue"].(string)
				stderr.WriteString(fmt.Sprintf("%s: %s", ename, evalue))
			}
		case "execute_reply":
			replyStatus, _ := msg.Content["status"].(string)
			if status == "" {
				status = replyStatus
			}
			if ec, ok := msg.Content["execution_count"].(float64); ok {
				execCount = int(ec)
			}
			goto done
		}
	}
done:

	// Update last used
	sess.LastUsed = time.Now()
	sessions.Store(sessionID, sess)

	result := map[string]any{
		"session_id":      sessionID,
		"status":          status,
		"stdout":          stdout.String(),
		"stderr":          stderr.String(),
		"execution_count": execCount,
	}
	if displayData != nil {
		result["display_data"] = displayData
	}
	if len(traceback) > 0 {
		result["traceback"] = traceback
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return toolResult(string(data)), nil
}

func handleListSessions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var list []kernelSession
	sessions.Range(func(key, value any) bool {
		list = append(list, value.(kernelSession))
		return true
	})
	data, _ := json.MarshalIndent(list, "", "  ")
	return toolResult(string(data)), nil
}

func handleStop(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return toolError("session_id is required"), nil
	}

	val, ok := sessions.Load(sessionID)
	if !ok {
		return toolError(fmt.Sprintf("session %q not found", sessionID)), nil
	}
	sess := val.(kernelSession)

	// DELETE /api/kernels/{id}
	httpReq, _ := http.NewRequestWithContext(ctx, "DELETE", jupyterURL+"/api/kernels/"+sess.KernelID, nil)
	if jupyterToken != "" {
		httpReq.Header.Set("Authorization", "token "+jupyterToken)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return toolError(fmt.Sprintf("stop kernel: %v", err)), nil
	}
	resp.Body.Close()

	sessions.Delete(sessionID)

	result, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"stopped":    true,
	})
	return toolResult(string(result)), nil
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(msg)},
		IsError: true,
	}
}

func toolResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(text)},
	}
}
