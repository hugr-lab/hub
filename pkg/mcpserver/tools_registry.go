package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerRegistryTools(mcpSrv *server.MCPServer, userID string) {
	mcpSrv.AddTool(
		mcp.NewTool("registry-save",
			mcp.WithDescription("Save a query to the registry for reuse."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Query name")),
			mcp.WithString("query", mcp.Required(), mcp.Description("GraphQL query text")),
			mcp.WithString("description", mcp.Description("What this query does")),
		),
		s.handleRegistrySave(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("registry-search",
			mcp.WithDescription("Search saved queries in the registry."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search text")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
		),
		s.handleRegistrySearch(userID),
	)
}

func (s *Server) handleRegistrySave(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		name, _ := args["name"].(string)
		query, _ := args["query"].(string)
		desc, _ := args["description"].(string)

		id := fmt.Sprintf("reg-%d", time.Now().UnixNano())
		res, err := s.hugrClient.Query(ctx,
			`mutation($id: String!, $uid: String!, $name: String!, $query: String!, $desc: String) {
				hub { db { insert_query_registry(
					data: { id: $id, user_id: $uid, name: $name, query: $query, description: $desc }
				) { id name } } }
			}`,
			map[string]any{"id": id, "uid": userID, "name": name, "query": query, "desc": desc},
		)
		if err != nil {
			return toolError(fmt.Sprintf("failed to save query: %v", err)), nil
		}
		defer res.Close()
		if res.Err() != nil {
			return toolError(fmt.Sprintf("save query graphql error: %v", res.Err())), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleRegistrySearch(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		query, _ := args["query"].(string)
		limit := 10
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		gql := fmt.Sprintf(`{
			hub { db { query_registry(
				filter: { _and: [
					{ user_id: { eq: "%s" } }
					{ _or: [
						{ name: { ilike: "%%%s%%" } }
						{ description: { ilike: "%%%s%%" } }
					]}
				]}
				limit: %d
				order_by: [{field: "usage_count", direction: DESC}]
			) { id name query description tags usage_count } } }
		}`, userID, query, query, limit)

		res, err := s.hugrClient.Query(ctx, gql, nil)
		if err != nil {
			return toolError(fmt.Sprintf("registry search failed: %v", err)), nil
		}
		defer res.Close()
		if res.Err() != nil {
			return toolError(fmt.Sprintf("registry search graphql error: %v", res.Err())), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}
