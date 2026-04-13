// result-store-mcp is a stdio MCP server that manages saved query results.
// It saves Arrow IPC data to disk, lists/describes/queries saved results,
// and provides DuckDB SQL access over saved files.
//
// Usage:
//
//	result-store-mcp                     # stdio mode (default)
//	RESULT_STORE_PATH=/path/to/results   # override results directory
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"database/sql"

	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/duckdb/duckdb-go/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var resultsDir string

func main() {
	resultsDir = os.Getenv("RESULT_STORE_PATH")
	if resultsDir == "" {
		home := os.Getenv("HUB_AGENT_HOME")
		if home == "" {
			home = "."
		}
		resultsDir = filepath.Join(home, "results")
	}
	os.MkdirAll(resultsDir, 0o755)

	srv := server.NewMCPServer("result-store-mcp", "0.1.0",
		server.WithToolCapabilities(true),
	)

	srv.AddTool(mcp.NewTool("result.save",
		mcp.WithDescription("Save data to disk as Arrow file. Source can be inline JSON rows or a file path."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name for the saved result")),
		mcp.WithObject("rows", mcp.Description("JSON array of row objects to save")),
		mcp.WithString("path", mcp.Description("Path to existing Arrow IPC file to copy")),
	), handleSave)

	srv.AddTool(mcp.NewTool("result.list",
		mcp.WithDescription("List all saved results with names, row counts, and sizes."),
	), handleList)

	srv.AddTool(mcp.NewTool("result.describe",
		mcp.WithDescription("Get schema and stats for a saved result (no data returned)."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Result name")),
	), handleDescribe)

	srv.AddTool(mcp.NewTool("result.head",
		mcp.WithDescription("Get first N rows from a saved result."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Result name")),
		mcp.WithNumber("n", mcp.Description("Number of rows (default 10)")),
	), handleHead)

	srv.AddTool(mcp.NewTool("result.query",
		mcp.WithDescription("Run SQL query over a saved result using DuckDB. Reference the result by its name as table name."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Result name")),
		mcp.WithString("sql", mcp.Required(), mcp.Description("SQL query (use result name as table)")),
	), handleQuery)

	srv.AddTool(mcp.NewTool("result.drop",
		mcp.WithDescription("Delete a saved result from disk."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Result name")),
	), handleDrop)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	stdio := server.NewStdioServer(srv)
	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		log.Fatalf("result-store-mcp: %v", err)
	}
}

// handleSave saves rows as a JSON file (Arrow IPC streaming from Hugr
// multipart will be added when integrated with data-query save_as).
func handleSave(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, _ := args["name"].(string)
	if name == "" {
		return toolError("name is required"), nil
	}

	// Sanitize name
	name = sanitizeName(name)

	// Option 1: Copy from existing Arrow file
	if srcPath, ok := args["path"].(string); ok && srcPath != "" {
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return toolError(fmt.Sprintf("read source: %v", err)), nil
		}
		dstPath := filepath.Join(resultsDir, name+".arrow")
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return toolError(fmt.Sprintf("write result: %v", err)), nil
		}
		info, _ := os.Stat(dstPath)
		return toolResult(fmt.Sprintf(`{"name": %q, "bytes": %d, "source": "arrow_copy"}`, name, info.Size())), nil
	}

	// Option 2: Save inline JSON rows
	if rowsRaw, ok := args["rows"]; ok && rowsRaw != nil {
		data, err := json.MarshalIndent(rowsRaw, "", "  ")
		if err != nil {
			return toolError(fmt.Sprintf("marshal rows: %v", err)), nil
		}
		dstPath := filepath.Join(resultsDir, name+".json")
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return toolError(fmt.Sprintf("write result: %v", err)), nil
		}

		// Count rows
		var rows []any
		json.Unmarshal(data, &rows)
		return toolResult(fmt.Sprintf(`{"name": %q, "rows": %d, "bytes": %d, "format": "json"}`, name, len(rows), len(data))), nil
	}

	return toolError("either 'rows' (JSON array) or 'path' (Arrow file) is required"), nil
}

// handleList lists all saved results.
func handleList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return toolResult("[]"), nil
	}

	type resultInfo struct {
		Name      string `json:"name"`
		Format    string `json:"format"`
		Bytes     int64  `json:"bytes"`
		CreatedAt string `json:"created_at"`
	}

	var results []resultInfo
	for _, e := range entries {
		if e.IsDir() {
			// Multi-part result directory
			dirPath := filepath.Join(resultsDir, e.Name())
			var totalBytes int64
			parts, _ := os.ReadDir(dirPath)
			for _, p := range parts {
				if info, err := p.Info(); err == nil {
					totalBytes += info.Size()
				}
			}
			info, _ := e.Info()
			results = append(results, resultInfo{
				Name:      e.Name(),
				Format:    "multipart",
				Bytes:     totalBytes,
				CreatedAt: info.ModTime().Format(time.RFC3339),
			})
			continue
		}

		ext := filepath.Ext(e.Name())
		if ext != ".arrow" && ext != ".json" && ext != ".parquet" {
			continue
		}
		info, _ := e.Info()
		results = append(results, resultInfo{
			Name:      strings.TrimSuffix(e.Name(), ext),
			Format:    strings.TrimPrefix(ext, "."),
			Bytes:     info.Size(),
			CreatedAt: info.ModTime().Format(time.RFC3339),
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].CreatedAt > results[j].CreatedAt })

	data, _ := json.MarshalIndent(results, "", "  ")
	return toolResult(string(data)), nil
}

// handleDescribe returns schema and stats for a saved result.
func handleDescribe(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, _ := args["name"].(string)
	if name == "" {
		return toolError("name is required"), nil
	}
	name = sanitizeName(name)

	// Try Arrow first
	arrowPath := filepath.Join(resultsDir, name+".arrow")
	if info, err := os.Stat(arrowPath); err == nil {
		schema, rowCount := describeArrow(arrowPath)
		result := map[string]any{
			"name":       name,
			"format":     "arrow",
			"bytes":      info.Size(),
			"row_count":  rowCount,
			"schema":     schema,
			"created_at": info.ModTime().Format(time.RFC3339),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return toolResult(string(data)), nil
	}

	// Try JSON
	jsonPath := filepath.Join(resultsDir, name+".json")
	if info, err := os.Stat(jsonPath); err == nil {
		raw, _ := os.ReadFile(jsonPath)
		var rows []any
		json.Unmarshal(raw, &rows)
		result := map[string]any{
			"name":       name,
			"format":     "json",
			"bytes":      info.Size(),
			"row_count":  len(rows),
			"created_at": info.ModTime().Format(time.RFC3339),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return toolResult(string(data)), nil
	}

	// Try directory (multipart)
	dirPath := filepath.Join(resultsDir, name)
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		parts, _ := os.ReadDir(dirPath)
		var partNames []string
		var totalBytes int64
		for _, p := range parts {
			if pi, err := p.Info(); err == nil {
				partNames = append(partNames, p.Name())
				totalBytes += pi.Size()
			}
		}
		result := map[string]any{
			"name":       name,
			"format":     "multipart",
			"bytes":      totalBytes,
			"parts":      partNames,
			"created_at": info.ModTime().Format(time.RFC3339),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return toolResult(string(data)), nil
	}

	return toolError(fmt.Sprintf("result %q not found", name)), nil
}

// handleHead returns first N rows from a result.
func handleHead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, _ := args["name"].(string)
	if name == "" {
		return toolError("name is required"), nil
	}
	name = sanitizeName(name)
	n := 10
	if nf, ok := args["n"].(float64); ok && nf > 0 {
		n = int(nf)
	}

	// Try JSON
	jsonPath := filepath.Join(resultsDir, name+".json")
	if _, err := os.Stat(jsonPath); err == nil {
		raw, _ := os.ReadFile(jsonPath)
		var rows []any
		json.Unmarshal(raw, &rows)
		if n > len(rows) {
			n = len(rows)
		}
		data, _ := json.MarshalIndent(rows[:n], "", "  ")
		return toolResult(string(data)), nil
	}

	// Try Arrow
	arrowPath := filepath.Join(resultsDir, name+".arrow")
	if _, err := os.Stat(arrowPath); err == nil {
		rows := readArrowHead(arrowPath, n)
		data, _ := json.MarshalIndent(rows, "", "  ")
		return toolResult(string(data)), nil
	}

	return toolError(fmt.Sprintf("result %q not found", name)), nil
}

// handleQuery runs SQL over a saved result using embedded DuckDB.
func handleQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, _ := args["name"].(string)
	sqlQuery, _ := args["sql"].(string)
	if name == "" || sqlQuery == "" {
		return toolError("name and sql are required"), nil
	}
	name = sanitizeName(name)

	// Find the result file(s) and build CREATE VIEW
	var tableDef string
	arrowPath := filepath.Join(resultsDir, name+".arrow")
	jsonPath := filepath.Join(resultsDir, name+".json")
	dirPath := filepath.Join(resultsDir, name)

	if _, err := os.Stat(arrowPath); err == nil {
		tableDef = fmt.Sprintf("CREATE VIEW %s AS SELECT * FROM read_ipc('%s')", name, arrowPath)
	} else if _, err := os.Stat(jsonPath); err == nil {
		tableDef = fmt.Sprintf("CREATE VIEW %s AS SELECT * FROM read_json_auto('%s')", name, jsonPath)
	} else if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		tableDef = fmt.Sprintf("CREATE VIEW %s AS SELECT * FROM read_ipc('%s/*.arrow')", name, dirPath)
	} else {
		return toolError(fmt.Sprintf("result %q not found", name)), nil
	}

	// Execute via embedded DuckDB
	connector, err := duckdb.NewConnector("", nil)
	if err != nil {
		return toolError(fmt.Sprintf("DuckDB init: %v", err)), nil
	}
	defer connector.Close()

	db := sql.OpenDB(connector)
	defer db.Close()

	// Create view for the saved result
	if _, err := db.ExecContext(ctx, tableDef); err != nil {
		return toolError(fmt.Sprintf("DuckDB create view: %v", err)), nil
	}

	// Execute user query
	dbRows, err := db.QueryContext(ctx, sqlQuery)
	if err != nil {
		return toolError(fmt.Sprintf("DuckDB query: %v", err)), nil
	}
	defer dbRows.Close()

	// Convert to JSON
	cols, _ := dbRows.Columns()
	var rows []map[string]any
	for dbRows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := dbRows.Scan(ptrs...); err != nil {
			continue
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = values[i]
		}
		rows = append(rows, row)
	}

	data, _ := json.MarshalIndent(rows, "", "  ")
	return toolResult(string(data)), nil
}

// handleDrop deletes a saved result.
func handleDrop(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, _ := args["name"].(string)
	if name == "" {
		return toolError("name is required"), nil
	}
	name = sanitizeName(name)

	dropped := false
	for _, ext := range []string{".arrow", ".json", ".parquet"} {
		p := filepath.Join(resultsDir, name+ext)
		if err := os.Remove(p); err == nil {
			dropped = true
		}
	}
	// Try directory
	dirPath := filepath.Join(resultsDir, name)
	if err := os.RemoveAll(dirPath); err == nil {
		dropped = true
	}

	if !dropped {
		return toolError(fmt.Sprintf("result %q not found", name)), nil
	}
	return toolResult(fmt.Sprintf(`{"name": %q, "dropped": true}`, name)), nil
}

// ── helpers ──

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "..", "_")
	return name
}

func describeArrow(path string) ([]map[string]string, int) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()

	reader, err := ipc.NewReader(f)
	if err != nil {
		return nil, 0
	}
	defer reader.Release()

	schema := reader.Schema()
	var fields []map[string]string
	for _, field := range schema.Fields() {
		fields = append(fields, map[string]string{
			"name": field.Name,
			"type": field.Type.String(),
		})
	}

	rowCount := 0
	for reader.Next() {
		rec := reader.Record()
		rowCount += int(rec.NumRows())
	}
	return fields, rowCount
}

func readArrowHead(path string, n int) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	reader, err := ipc.NewReader(f)
	if err != nil {
		return nil
	}
	defer reader.Release()

	var rows []map[string]any
	schema := reader.Schema()

	for reader.Next() && len(rows) < n {
		rec := reader.Record()
		for i := 0; i < int(rec.NumRows()) && len(rows) < n; i++ {
			row := make(map[string]any)
			for j, field := range schema.Fields() {
				col := rec.Column(j)
				if col.IsNull(i) {
					row[field.Name] = nil
				} else {
					row[field.Name] = fmt.Sprintf("%v", col.ValueStr(i))
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
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
