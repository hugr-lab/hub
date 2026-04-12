---
name: kernel
description: >
  Execute code in Jupyter kernels — Python, DuckDB, or Hugr GraphQL.
  Only available in workspace context. Use when the user wants to run code,
  perform computations, create visualizations, or process data programmatically.
  Trigger on: run code, execute, python, script, compute, calculate, plot, 
  jupyter, kernel, notebook, pandas, matplotlib, duckdb sql.
context: local
mcp_backend: stdio
mcp_executable: kernel-mcp
tools:
  - name: kernel.start_session
  - name: kernel.execute
  - name: kernel.list_sessions
  - name: kernel.stop
---

# Kernel Execution

Execute code in Jupyter kernel sessions within the workspace.

## Available Kernels

| Kind | Language | Use for |
|------|----------|---------|
| `python` | Python 3 | Data analysis, visualization, pandas, matplotlib |
| `duckdb` | SQL (DuckDB) | Analytical SQL queries over local files |
| `hugr` | GraphQL | Direct Hugr GraphQL queries with kernel features |

## Workflow

1. **Start**: `kernel.start_session(kind: "python")` — creates a new session
2. **Execute**: `kernel.execute(session_id, code)` — run code, get output
3. **Continue**: Same session preserves state (variables, imports persist)
4. **Stop**: `kernel.stop(session_id)` — release resources when done

## Best Practices

- Reuse sessions — don't start a new one for every code block
- Import libraries in the first execution, then use them in subsequent ones
- For large data processing, combine with result store — save results to disk
- Use `print()` for output you want to see (kernel returns stdout)
- Handle errors gracefully — if execution fails, check stderr in the response
