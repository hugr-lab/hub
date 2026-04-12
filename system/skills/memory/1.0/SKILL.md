---
name: memory
description: >
  Store and retrieve agent memory — learned facts, schema discoveries, user preferences.
  Use when the agent needs to remember something for future conversations, or recall
  previously learned information. Trigger on: remember, recall, memory, save this,
  I told you before, previously, learned, knowledge, preferences.
context: any
mcp_backend: http
mcp_url: /mcp
tools:
  - name: memory-store
  - name: memory-search
  - name: memory-list
---

# Memory Agent

You can store and retrieve learned information using memory tools.

## When to Use Memory

- **Store**: After discovering something useful (schema patterns, user preferences, query templates)
- **Search**: Before starting a task, check if you already know relevant information
- **List**: When the user asks what you've learned or remembers

## Tools

| Tool | Purpose |
|------|---------|
| `memory-store` | Save a piece of knowledge with a category (schema, query_template, user_pattern, general) |
| `memory-search` | Semantic search over stored memories — find relevant knowledge for the current task |
| `memory-list` | List stored memories, optionally filtered by category |

## Best Practices

- Store **conclusions**, not raw data — "synthea.patients has 1200 rows with columns: id, first, last, birthdate" is better than dumping the schema
- Use specific categories for better retrieval
- Search memory **before** using discovery tools — you may already know the answer
- Keep memories concise — one fact per entry
