package agent

import (
	"context"
	"fmt"
	"strings"
)

// Learner handles continuous learning: stores schema discoveries, query patterns,
// and retrieves relevant context before LLM calls.
type Learner struct {
	agent *Agent
}

func NewLearner(agent *Agent) *Learner {
	return &Learner{agent: agent}
}

// schemaTools are MCP tools whose results contain schema information worth remembering.
var schemaTools = map[string]bool{
	"get-schema":     true,
	"list-tables":    true,
	"describe-table": true,
	"list-fields":    true,
}

// queryTools are MCP tools whose results indicate a successful query execution.
var queryTools = map[string]bool{
	"query-data":    true,
	"execute-query": true,
}

// LearnFromToolCall analyzes a successful tool call result and stores relevant
// knowledge in memory (schemas) or registry (query patterns).
func (l *Learner) LearnFromToolCall(ctx context.Context, toolName string, args map[string]any, result string) {
	if result == "" {
		return
	}

	switch {
	case schemaTools[toolName]:
		l.storeSchemaMemory(ctx, toolName, args, result)
	case queryTools[toolName]:
		l.storeQueryPattern(ctx, toolName, args, result)
	}
}

// storeSchemaMemory stores schema discovery results as memory entries.
func (l *Learner) storeSchemaMemory(ctx context.Context, toolName string, args map[string]any, result string) {
	// Build a descriptive summary for the embedding
	var parts []string
	parts = append(parts, fmt.Sprintf("Tool: %s", toolName))
	if table, ok := args["table"].(string); ok {
		parts = append(parts, fmt.Sprintf("Table: %s", table))
	}
	if schema, ok := args["schema"].(string); ok {
		parts = append(parts, fmt.Sprintf("Schema: %s", schema))
	}
	if ds, ok := args["data_source"].(string); ok {
		parts = append(parts, fmt.Sprintf("DataSource: %s", ds))
	}

	content := strings.Join(parts, "\n") + "\n\nResult:\n" + truncate(result, 2000)

	_, err := l.agent.CallTool(ctx, "memory-store", map[string]any{
		"content":  content,
		"category": "schema",
		"source":   toolName,
	})
	if err != nil {
		l.agent.logger.Warn("failed to store schema memory", "tool", toolName, "error", err)
	}
}

// storeQueryPattern saves a successful query to the registry for future reuse.
func (l *Learner) storeQueryPattern(ctx context.Context, toolName string, args map[string]any, result string) {
	query, _ := args["query"].(string)
	if query == "" {
		return
	}

	name := queryPatternName(query)
	desc := fmt.Sprintf("Auto-learned from %s. Result preview: %s", toolName, truncate(result, 200))

	_, err := l.agent.CallTool(ctx, "registry-save", map[string]any{
		"name":        name,
		"query":       query,
		"description": desc,
	})
	if err != nil {
		l.agent.logger.Warn("failed to store query pattern", "tool", toolName, "error", err)
	}
}

// RetrieveContext loads relevant memories and registry entries for the given user message.
// Returns formatted context string to include in the LLM prompt.
func (l *Learner) RetrieveContext(ctx context.Context, userMessage string) string {
	var sections []string

	// Search memories (schemas, patterns)
	memories, err := l.agent.CallTool(ctx, "memory-search", map[string]any{
		"query": userMessage,
		"limit": float64(5),
	})
	if err == nil && memories != "" && memories != "null" {
		sections = append(sections, "## Relevant memories\n"+memories)
	}

	// Search query registry
	registry, err := l.agent.CallTool(ctx, "registry-search", map[string]any{
		"query": userMessage,
		"limit": float64(3),
	})
	if err == nil && registry != "" && registry != "null" {
		sections = append(sections, "## Saved query patterns\n"+registry)
	}

	return strings.Join(sections, "\n\n")
}

// queryPatternName extracts a short name from a GraphQL query.
func queryPatternName(query string) string {
	// Try to extract the first meaningful word after { and any module nesting
	q := strings.TrimSpace(query)
	q = strings.TrimPrefix(q, "query")
	q = strings.TrimPrefix(q, "mutation")

	// Find the innermost operation name
	depth := 0
	var lastWord strings.Builder
	for _, ch := range q {
		switch ch {
		case '{':
			depth++
			lastWord.Reset()
		case '}', '(', ' ', '\n', '\t':
			// keep last word
		default:
			lastWord.WriteRune(ch)
		}
		if depth > 0 && (ch == '(' || ch == ' ' || ch == '\n') && lastWord.Len() > 0 {
			break
		}
	}

	name := lastWord.String()
	if name == "" {
		name = "auto-query"
	}
	return name
}

// truncate cuts a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
