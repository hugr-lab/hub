# 001 — Analytics Hub: Target Architecture

**Date created**: 2026-03-23
**Status**: Draft

## Description

Corporate analytics hub built on JupyterHub with five stages: workspaces with Jupyter kernels (OIDC auth), Hub Service as Hugr application (airport-go), AI agent with sandboxed tools, interactive Dives, and scheduler.

## Artifacts

| File | Description |
| ---- | ----------- |
| `research.md` | Deep analysis of hugr-lab ecosystem, JupyterHub OIDC, MotherDuck Dives, OpenClaw, vector search |
| `design.md` | Target architecture: auth model (OIDC + management secret), components, data flows |
| `user-stories.md` | User stories for Admin, User, and Analyst roles with story map |
| `stages.md` | Five implementation stages with requirements summary |
| `stage-1-spec.md` | Detailed specification for Stage 1 (JupyterHub + OIDC + managed connection + token refresh) |
