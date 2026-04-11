package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerConversationTools(mcpSrv *server.MCPServer, userID string) {
	mcpSrv.AddTool(
		mcp.NewTool("conversation-create",
			mcp.WithDescription("Create a new conversation."),
			mcp.WithString("title", mcp.Description("Conversation title (default: New Chat)")),
			mcp.WithString("mode", mcp.Required(), mcp.Description("Chat mode: llm, tools, or agent")),
			mcp.WithString("folder", mcp.Description("Folder for grouping")),
			mcp.WithString("agent_id", mcp.Description("Agent ID (for mode=agent)")),
			mcp.WithString("model", mcp.Description("LLM model name (for mode=llm)")),
		),
		s.handleConversationCreate(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("conversation-list",
			mcp.WithDescription("List conversations for current user."),
			mcp.WithString("folder", mcp.Description("Filter by folder")),
			mcp.WithBoolean("include_deleted", mcp.Description("Include soft-deleted (default: false)")),
		),
		s.handleConversationList(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("conversation-rename",
			mcp.WithDescription("Rename a conversation."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Conversation ID")),
			mcp.WithString("title", mcp.Required(), mcp.Description("New title")),
		),
		s.handleConversationRename(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("conversation-delete",
			mcp.WithDescription("Soft-delete a conversation (hidden from UI, messages retained)."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Conversation ID")),
		),
		s.handleConversationDelete(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("conversation-messages",
			mcp.WithDescription("Load messages for a conversation (paginated, newest first)."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Conversation ID")),
			mcp.WithNumber("limit", mcp.Description("Max messages (default: 50)")),
			mcp.WithString("before", mcp.Description("Cursor: messages before this ISO timestamp")),
		),
		s.handleConversationMessages(userID),
	)
}

func (s *Server) handleConversationCreate(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		mode, _ := args["mode"].(string)
		if mode != "llm" && mode != "tools" && mode != "agent" {
			return toolError("mode must be llm, tools, or agent"), nil
		}

		title, _ := args["title"].(string)
		if title == "" {
			title = "New Chat"
		}

		id := fmt.Sprintf("conv-%d", time.Now().UnixNano())

		vars := map[string]any{
			"id":    id,
			"uid":   userID,
			"title": title,
			"mode":  mode,
		}

		gql := `mutation($id: String!, $uid: String!, $title: String!, $mode: String!) {
			hub { db { insert_conversations(data: {
				id: $id, user_id: $uid, title: $title, mode: $mode
			}) { id } } }
		}`

		// Add optional fields
		if folder, ok := args["folder"].(string); ok && folder != "" {
			vars["folder"] = folder
			gql = `mutation($id: String!, $uid: String!, $title: String!, $mode: String!, $folder: String!) {
				hub { db { insert_conversations(data: {
					id: $id, user_id: $uid, title: $title, mode: $mode, folder: $folder
				}) { id } } }
			}`
		}

		res, err := s.hugrClient.Query(ctx, gql, vars)
		if err != nil {
			return toolError(fmt.Sprintf("create conversation: %v", err)), nil
		}
		defer res.Close()
		if res.Err() != nil {
			return toolError(fmt.Sprintf("create conversation: %v", res.Err())), nil
		}

		result, _ := json.Marshal(map[string]any{
			"id": id, "title": title, "mode": mode,
		})
		return toolResult(string(result)), nil
	}
}

func (s *Server) handleConversationList(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		includeDeleted, _ := request.GetArguments()["include_deleted"].(bool)

		filterParts := `user_id: { eq: $uid }`
		varDefs := `$uid: String!`
		vars := map[string]any{"uid": userID}

		if !includeDeleted {
			filterParts += `, deleted_at: { is_null: true }`
		}

		gql := fmt.Sprintf(`query(%s) {
			hub { db { conversations(
				filter: { %s }
				order_by: [{field: "updated_at", direction: DESC}]
			) {
				id title folder mode agent_id model updated_at created_at
			} } }
		}`, varDefs, filterParts)

		res, err := s.hugrClient.Query(ctx, gql, vars)
		if err != nil {
			return toolError(fmt.Sprintf("list conversations: %v", err)), nil
		}
		defer res.Close()
		if res.Err() != nil {
			return toolError(fmt.Sprintf("list conversations: %v", res.Err())), nil
		}

		var convs []any
		if err := res.ScanData("hub.db.conversations", &convs); err != nil {
			// Empty result is not an error — return empty array
			return toolResult("[]"), nil
		}
		if convs == nil {
			convs = []any{}
		}

		data, _ := json.MarshalIndent(convs, "", "  ")
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleConversationRename(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		id, _ := args["id"].(string)
		title, _ := args["title"].(string)
		if id == "" || title == "" {
			return toolError("id and title required"), nil
		}

		res, err := s.hugrClient.Query(ctx,
			`mutation($id: String!, $title: String!) {
				hub { db { update_conversations(
					filter: { id: { eq: $id } }
					data: { title: $title }
				) { affected_rows } } }
			}`,
			map[string]any{"id": id, "title": title},
		)
		if err != nil {
			return toolError(fmt.Sprintf("rename: %v", err)), nil
		}
		defer res.Close()

		return toolResult(fmt.Sprintf(`{"renamed": "%s"}`, id)), nil
	}
}

func (s *Server) handleConversationDelete(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.GetArguments()["id"].(string)
		if id == "" {
			return toolError("id required"), nil
		}

		res, err := s.hugrClient.Query(ctx,
			`mutation($id: String!) {
				hub { db { update_conversations(
					filter: { id: { eq: $id } }
					data: { deleted_at: "NOW()" }
				) { affected_rows } } }
			}`,
			map[string]any{"id": id},
		)
		if err != nil {
			return toolError(fmt.Sprintf("delete: %v", err)), nil
		}
		defer res.Close()

		return toolResult(fmt.Sprintf(`{"deleted": "%s"}`, id)), nil
	}
}

func (s *Server) handleConversationMessages(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		id, _ := args["id"].(string)
		if id == "" {
			return toolError("id required"), nil
		}

		limit := 50
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		vars := map[string]any{
			"cid":   id,
			"limit": limit,
		}
		varDefs := `$cid: String!, $limit: Int!`
		filterPart := `conversation_id: { eq: $cid }`

		if before, ok := args["before"].(string); ok && before != "" {
			filterPart += `, created_at: { lt: $before }`
			varDefs += `, $before: Timestamp!`
			vars["before"] = before
		}

		gql := fmt.Sprintf(`query(%s) {
			hub { db { agent_messages(
				filter: { %s }
				order_by: [{field: "created_at", direction: DESC}]
				limit: $limit
			) {
				id role content tool_calls tool_call_id tokens_used model created_at
			} } }
		}`, varDefs, filterPart)

		res, err := s.hugrClient.Query(ctx, gql, vars)
		if err != nil {
			return toolError(fmt.Sprintf("messages: %v", err)), nil
		}
		defer res.Close()
		if res.Err() != nil {
			return toolError(fmt.Sprintf("messages: %v", res.Err())), nil
		}

		var msgs []any
		if err := res.ScanData("hub.db.agent_messages", &msgs); err != nil {
			return toolResult("[]"), nil
		}
		if msgs == nil {
			msgs = []any{}
		}

		data, _ := json.MarshalIndent(msgs, "", "  ")
		return toolResult(string(data)), nil
	}
}
