package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerHugrTools registers Hugr discovery and data tools on the MCP server.
// All tools forward to Hugr GraphQL with management auth + user identity.
func (s *Server) registerHugrTools(mcpSrv *server.MCPServer, userID string) {
	// data-graphql_query — execute arbitrary GraphQL query
	mcpSrv.AddTool(
		mcp.NewTool("data-graphql_query",
			mcp.WithDescription("Execute a GraphQL query against Hugr data sources. Returns JSON result."),
			mcp.WithString("query", mcp.Required(), mcp.Description("GraphQL query string")),
			mcp.WithString("variables", mcp.Description("JSON object with query variables")),
		),
		s.handleGraphQLQuery(userID),
	)

	// data-validate_graphql_query — validate without executing
	mcpSrv.AddTool(
		mcp.NewTool("data-validate_graphql_query",
			mcp.WithDescription("Validate a GraphQL query without executing it. Returns validation errors if any."),
			mcp.WithString("query", mcp.Required(), mcp.Description("GraphQL query to validate")),
		),
		s.handleValidateQuery(userID),
	)

	// discovery-search_modules — find data modules
	mcpSrv.AddTool(
		mcp.NewTool("discovery-search_modules",
			mcp.WithDescription("Search for data modules (schemas/catalogs) by natural language query."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		),
		s.handleDiscoveryQuery(userID, "search_modules"),
	)

	// discovery-search_data_sources — find data sources
	mcpSrv.AddTool(
		mcp.NewTool("discovery-search_data_sources",
			mcp.WithDescription("Search for data sources by query."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		),
		s.handleDiscoveryQuery(userID, "search_data_sources"),
	)

	// discovery-search_module_data_objects — find tables/views in a module
	mcpSrv.AddTool(
		mcp.NewTool("discovery-search_module_data_objects",
			mcp.WithDescription("Search for tables and views within a module."),
			mcp.WithString("module", mcp.Required(), mcp.Description("Module name")),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		),
		s.handleDiscoveryModuleQuery(userID, "search_module_data_objects"),
	)

	// schema-type_fields — get fields of a type
	mcpSrv.AddTool(
		mcp.NewTool("schema-type_fields",
			mcp.WithDescription("Get all fields of a GraphQL type with their types and descriptions."),
			mcp.WithString("type", mcp.Required(), mcp.Description("Type name (e.g. northwind_orders)")),
		),
		s.handleSchemaQuery(userID, "type_fields"),
	)

	// schema-type_info — get type metadata
	mcpSrv.AddTool(
		mcp.NewTool("schema-type_info",
			mcp.WithDescription("Get metadata for a GraphQL type (description, directives, module)."),
			mcp.WithString("type", mcp.Required(), mcp.Description("Type name")),
		),
		s.handleSchemaQuery(userID, "type_info"),
	)
}

func (s *Server) handleGraphQLQuery(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := request.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		var vars map[string]any
		if varsStr, ok := request.GetArguments()["variables"].(string); ok && varsStr != "" {
			if err := json.Unmarshal([]byte(varsStr), &vars); err != nil {
				return toolError(fmt.Sprintf("invalid variables JSON: %v", err)), nil
			}
		}

		res, err := s.hugrClient.Query(ctx, query, vars)
		if err != nil {
			return toolError(fmt.Sprintf("query failed: %v", err)), nil
		}

		data, err := json.MarshalIndent(res.Data, "", "  ")
		if err != nil {
			return toolError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}

		s.logger.Info("graphql query executed", "user", userID, "query_len", len(query))
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleValidateQuery(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := request.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		// Use Hugr's validate endpoint
		res, err := s.hugrClient.Query(ctx,
			`query($q: String!) { core { validate_query(query: $q) { valid errors } } }`,
			map[string]any{"q": query},
		)
		if err != nil {
			return toolError(fmt.Sprintf("validation failed: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleDiscoveryQuery(userID, tool string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := request.GetArguments()["query"].(string)

		var gql string
		switch tool {
		case "search_modules":
			gql = `query($q: String!) { core { modules(filter: { name: { ilike: $q } }) { name description } } }`
		case "search_data_sources":
			gql = `query($q: String!) { core { data_sources(filter: { name: { ilike: $q } }) { name type description } } }`
		default:
			return toolError("unknown discovery tool"), nil
		}

		res, err := s.hugrClient.Query(ctx, gql, map[string]any{"q": "%" + query + "%"})
		if err != nil {
			return toolError(fmt.Sprintf("discovery failed: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleDiscoveryModuleQuery(userID, tool string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		module, _ := request.GetArguments()["module"].(string)
		query, _ := request.GetArguments()["query"].(string)

		gql := `query($m: String!, $q: String!) { core { types(filter: { _and: [{ module: { eq: $m } }, { name: { ilike: $q } }] }) { name description module } } }`

		res, err := s.hugrClient.Query(ctx, gql, map[string]any{"m": module, "q": "%" + query + "%"})
		if err != nil {
			return toolError(fmt.Sprintf("discovery failed: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}

func (s *Server) handleSchemaQuery(userID, tool string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		typeName, _ := request.GetArguments()["type"].(string)

		var gql string
		switch tool {
		case "type_fields":
			gql = `query($t: String!) { core { type_fields(filter: { type_name: { eq: $t } }) { name type description is_nullable } } }`
		case "type_info":
			gql = `query($t: String!) { core { types(filter: { name: { eq: $t } }) { name description module } } }`
		default:
			return toolError("unknown schema tool"), nil
		}

		res, err := s.hugrClient.Query(ctx, gql, map[string]any{"t": typeName})
		if err != nil {
			return toolError(fmt.Sprintf("schema query failed: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}
