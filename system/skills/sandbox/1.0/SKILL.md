---
name: sandbox
description: >
  Execute Python and bash code in a sandboxed environment. Use when the user
  asks to run scripts, process files, install packages, or perform computations
  that don't require a Jupyter kernel session.
  Trigger on: run, execute, script, bash, shell, python, pip install, file,
  process, compute, calculate, transform.
context: any
mcp_backend: stdio
mcp_executable: sandbox-mcp
tools:
  - name: sandbox-python
  - name: sandbox-bash
---

# Sandbox Execution

Run Python and bash code in an isolated sandbox.

## Tools

| Tool | Purpose |
|------|---------|
| `sandbox-python` | Execute Python code with stdout/stderr capture |
| `sandbox-bash` | Execute bash commands with output capture |

## When to Use

- Quick computations that don't need persistent kernel state
- File operations (read, write, transform)
- Package installation or system commands
- Data transformations that don't need DuckDB or Hugr

## When to Use Kernel Instead

- Multi-step analysis with state (variables persist across executions)
- Jupyter-specific features (display, widgets, rich output)
- Working with hugr-kernel or duckdb-kernel directly
