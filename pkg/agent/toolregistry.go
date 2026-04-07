package agent

import (
	"encoding/json"

	"github.com/hugr-lab/hub/pkg/llmrouter"
	"github.com/mark3labs/mcp-go/mcp"
)

const sourceHub = "hub"

// ToolRegistry provides a unified tool index across all MCP connections.
type ToolRegistry struct {
	tools  map[string]toolEntry // tool name → entry
	order  []string             // insertion order for stable listing
}

type toolEntry struct {
	Tool   mcp.Tool
	Source string // "hub" or local server name
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]toolEntry),
	}
}

// Register adds tools from an MCP source. First registration wins on collision.
func (r *ToolRegistry) Register(source string, tools []mcp.Tool) {
	for _, t := range tools {
		if _, exists := r.tools[t.Name]; exists {
			continue // first registered wins
		}
		r.tools[t.Name] = toolEntry{Tool: t, Source: source}
		r.order = append(r.order, t.Name)
	}
}

// Lookup returns the source server for a tool name.
func (r *ToolRegistry) Lookup(toolName string) (source string, found bool) {
	entry, ok := r.tools[toolName]
	if !ok {
		return "", false
	}
	return entry.Source, true
}

// AllTools returns all registered MCP tools in stable order.
func (r *ToolRegistry) AllTools() []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(r.order))
	for _, name := range r.order {
		tools = append(tools, r.tools[name].Tool)
	}
	return tools
}

// ToLLMTools converts MCP tool schemas to the format expected by llm-complete.
func (r *ToolRegistry) ToLLMTools() []llmrouter.Tool {
	result := make([]llmrouter.Tool, 0, len(r.order))
	for _, name := range r.order {
		entry := r.tools[name]
		// Skip llm-complete and llm-list-models — agent uses them internally, not as LLM tools
		if name == "llm-complete" || name == "llm-list-models" {
			continue
		}
		t := llmrouter.Tool{
			Name:        entry.Tool.Name,
			Description: entry.Tool.Description,
		}
		if entry.Tool.InputSchema.Properties != nil {
			t.Parameters = inputSchemaToMap(entry.Tool.InputSchema)
		}
		result = append(result, t)
	}
	return result
}

func inputSchemaToMap(schema mcp.ToolInputSchema) map[string]any {
	// Convert MCP input schema to JSON Schema map for LLM
	data, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}
