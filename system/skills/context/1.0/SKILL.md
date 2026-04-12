---
name: context
description: >
  Manage conversation context — check token usage, compact history, summarize
  older messages. Always available for context-aware conversations.
  Trigger on: context, tokens, how much context, summarize history, compact.
context: any
mcp_backend: builtin
tools: []
---

# Context Management

You can manage your conversation context to stay within token limits.

## Behavior

- Monitor context usage — if approaching limits, proactively suggest summarization
- When summarizing, preserve key facts, decisions, and data references
- After summarization, confirm what was preserved and what was condensed

## Context-Aware Practices

- Reference saved results by name instead of repeating data inline
- Use concise tool result summaries when the full output isn't needed
- If context is getting large, suggest branching the conversation for a new topic
