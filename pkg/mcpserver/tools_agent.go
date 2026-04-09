package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerAgentTools(mcpSrv *server.MCPServer, userID string) {
	if s.agentConn == nil {
		return
	}

	mcpSrv.AddTool(
		mcp.NewTool("agent-message",
			mcp.WithDescription("Send a message to another agent instance. Use for inter-agent communication and task delegation."),
			mcp.WithString("target_instance_id", mcp.Required(), mcp.Description("Target agent instance ID")),
			mcp.WithString("message", mcp.Required(), mcp.Description("Message content to send")),
		),
		s.handleAgentMessage(userID),
	)
}

func (s *Server) handleAgentMessage(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.agentConn == nil {
			return toolError("agent connections not available"), nil
		}

		targetID, _ := request.GetArguments()["target_instance_id"].(string)
		if targetID == "" {
			return toolError("target_instance_id is required"), nil
		}
		message, _ := request.GetArguments()["message"].(string)
		if message == "" {
			return toolError("message is required"), nil
		}

		if !s.agentConn.IsConnected(targetID) {
			return toolError(fmt.Sprintf("agent %s is not connected", targetID)), nil
		}

		response, err := s.agentConn.SendMessage(ctx, targetID, "", userID, message)
		if err != nil {
			return toolError(fmt.Sprintf("agent-message failed: %v", err)), nil
		}

		s.logger.Info("agent-message sent", "from", userID, "to", targetID)
		return toolResult(response), nil
	}
}
