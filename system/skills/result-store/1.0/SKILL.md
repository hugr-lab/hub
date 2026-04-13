---
name: result-store
description: >
  Save query results to disk for later analysis without loading them into the conversation.
  Use when working with large datasets, saving intermediate results, or when the user wants
  to analyze data without re-fetching. Trigger on: save, save as, saved results, analyze saved,
  large dataset, too many rows, result store, list results, drop result, query result.
context: any
mcp_backend: stdio
mcp_executable: result-store-mcp
tools:
  - name: result.save
  - name: result.list
  - name: result.describe
  - name: result.head
  - name: result.query
  - name: result.drop
---

# Result Store

Save query results to disk and analyze them without loading data into the conversation context.

## When to Use

- Query returns many rows (>100) — save instead of displaying inline
- User wants to work with the same data across multiple questions
- Need to combine results from multiple queries for analysis

## Workflow

1. **Save**: After a data query, save results with `result.save`
2. **Describe**: Check schema and row count with `result.describe`
3. **Preview**: Look at first few rows with `result.head`
4. **Analyze**: Run SQL queries with `result.query` (DuckDB syntax)
5. **Clean up**: Remove unneeded results with `result.drop`

## Tools

| Tool | Purpose |
|------|---------|
| `result.save` | Save data to disk (from JSON rows or Arrow file path) |
| `result.list` | List all saved results with sizes |
| `result.describe` | Get schema and stats (no data) |
| `result.head` | Preview first N rows |
| `result.query` | Run DuckDB SQL over saved data |
| `result.drop` | Delete a saved result |

## Best Practices

- Give results descriptive names (e.g. "patient_demographics" not "data1")
- Save intermediate results to avoid re-querying
- Use `result.describe` before querying to understand the schema
- Clean up results when analysis is complete
