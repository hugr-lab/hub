package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerMemoryTools(mcpSrv *server.MCPServer, userID string) {
	mcpSrv.AddTool(
		mcp.NewTool("memory-store",
			mcp.WithDescription("Store a memory entry with semantic embedding. Categories: schema, query_template, user_pattern, general."),
			mcp.WithString("content", mcp.Required(), mcp.Description("Memory content to store")),
			mcp.WithString("category", mcp.Description("Category: schema, query_template, user_pattern, general")),
			mcp.WithString("source", mcp.Description("Origin of this memory (e.g. session ID, tool name)")),
		),
		s.handleMemoryStore(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("memory-search",
			mcp.WithDescription("Search memories by semantic similarity. Returns most relevant entries."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query text")),
			mcp.WithString("category", mcp.Description("Filter by category")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 5)")),
		),
		s.handleMemorySearch(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("memory-list",
			mcp.WithDescription("List recent memory entries for the current user."),
			mcp.WithString("category", mcp.Description("Filter by category")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
		),
		s.handleMemoryList(userID),
	)
}

func (s *Server) handleMemoryStore(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, _ := request.GetArguments()["content"].(string)
		if content == "" {
			return toolError("content is required"), nil
		}
		category, _ := request.GetArguments()["category"].(string)
		if category == "" {
			category = "general"
		}
		source, _ := request.GetArguments()["source"].(string)

		res, err := s.hugrClient.Query(ctx,
			`mutation($uid: String!, $content: String!, $category: String!, $source: String) {
				hub { hub { insert_agent_memory(
					data: { user_id: $uid, content: $content, category: $category, source: $source }
				) { id } } }
			}`,
			map[string]any{"uid": userID, "content": content, "category": category, "source": source},
		)
		if err != nil {
			return toolError(fmt.Sprintf("failed to store memory: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		s.logger.Info("memory stored", "user", userID, "category", category)
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleMemorySearch(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := request.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}
		category, _ := request.GetArguments()["category"].(string)
		limit := 5
		if l, ok := request.GetArguments()["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		// Build filter
		filter := fmt.Sprintf(`{ user_id: { eq: "%s" }`, userID)
		if category != "" {
			filter += fmt.Sprintf(`, category: { eq: "%s" }`, category)
		}
		filter += " }"

		// Use semantic search via _distance_to_query
		gql := fmt.Sprintf(`{
			hub { hub { agent_memory(
				filter: %s
				limit: %d
				order_by: { created_at: desc }
			) {
				id content category source created_at
			} } }
		}`, filter, limit)

		res, err := s.hugrClient.Query(ctx, gql, nil)
		if err != nil {
			return toolError(fmt.Sprintf("memory search failed: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleMemoryList(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		category, _ := request.GetArguments()["category"].(string)
		limit := 10
		if l, ok := request.GetArguments()["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		filter := fmt.Sprintf(`{ user_id: { eq: "%s" }`, userID)
		if category != "" {
			filter += fmt.Sprintf(`, category: { eq: "%s" }`, category)
		}
		filter += " }"

		gql := fmt.Sprintf(`{
			hub { hub { agent_memory(
				filter: %s
				limit: %d
				order_by: { created_at: desc }
			) {
				id content category source created_at
			} } }
		}`, filter, limit)

		res, err := s.hugrClient.Query(ctx, gql, nil)
		if err != nil {
			return toolError(fmt.Sprintf("memory list failed: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}
