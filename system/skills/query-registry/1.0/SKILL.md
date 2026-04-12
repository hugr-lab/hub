---
name: query-registry
description: >
  Save and recall useful GraphQL queries for reuse. Use when the user builds
  a complex query they might want to run again, or asks for a previously saved query.
  Trigger on: save query, saved queries, reuse, registry, template, bookmark query.
context: any
mcp_backend: http
mcp_url: /mcp
tools:
  - name: registry-save
  - name: registry-search
---

# Query Registry

Save and recall useful GraphQL queries for reuse.

## Tools

| Tool | Purpose |
|------|---------|
| `registry-save` | Save a GraphQL query with a name, description, and tags |
| `registry-search` | Search saved queries by keyword or description |

## When to Use

- After building a complex query that the user may want to run again
- When the user says "save this query" or "I want to reuse this"
- Before building a new query — check if a similar one already exists
