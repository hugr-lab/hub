import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

/* ────────────────────────────────────────────────────────────────────────
 * Agent types — `hub.agent.db.agent_types` (the hugen-owned canon).
 *
 * ONE row per type; `config` is a single JSON blob that serves BOTH planes:
 *   • hub reads `config.orchestration.{image,memory_bytes,nano_cpus,pids_limit}`
 *     for the container spawn (agentmgr) — `image` is REQUIRED or spawn fails.
 *   • hugen reads the runtime keys (models/skills/tool_providers/subagents/hitl/
 *     compactor/recap/permissions/…) — `models.model` is REQUIRED or boot fails.
 * hugen's loader is lenient (ignores unknown keys), so `orchestration` and any
 * other hub-only section coexist safely.
 *
 * create_agent validates its agent_type_id against THIS table (FK), so it is the
 * authoritative list of creatable types. The legacy `hub.db.agent_types` is not
 * used by the spawn and is intentionally ignored here.
 * ──────────────────────────────────────────────────────────────────────── */

export interface AgentType {
  id: string
  name: string
  description: string
  /** Full runtime + orchestration config as pretty JSON text. */
  config: string
}

export interface AgentTypeInput {
  id: string
  name: string
  description?: string
  /** JSON text; parsed to a JSON value on the wire. */
  config: string
}

interface RawAgentType {
  id: string
  name: string
  description: string | null
  config: unknown
}

const jsonText = (v: unknown): string =>
  v == null ? '{}' : typeof v === 'string' ? v : JSON.stringify(v, null, 2)

const parseConfig = (text: string): unknown => {
  const t = text.trim()
  if (!t) return {}
  return JSON.parse(t)
}

/* ── demo store ───────────────────────────────────────────────────────── */

const DEMO_CONFIG = `{
  "orchestration": { "image": "hugen:latest", "memory_bytes": 0, "nano_cpus": 0, "pids_limit": 0 },
  "models": { "mode": "remote", "model": "gemma4-26b", "routes": { "cheap": { "model": "gemma-small" } } },
  "skills": { "pin": ["hugr-data", "analyst"] }
}`

let demoTypes: AgentType[] = [
  {
    id: 'data-analyst',
    name: 'Data Analyst',
    description: 'Hugr data-exploration agent: discovery, query, and analysis skills over the mesh.',
    config: DEMO_CONFIG,
  },
  {
    id: 'hugen-analyst',
    name: 'Hugen Analyst',
    description: 'Remote-mode analyst agent (hub.agent.db)',
    config: DEMO_CONFIG,
  },
]

/* ── reads ────────────────────────────────────────────────────────────── */

export async function listAgentTypes(): Promise<AgentType[]> {
  return withDemo(
    () => demoTypes.map((t) => ({ ...t })).sort((a, b) => a.name.localeCompare(b.name)),
    async () => {
      const d = await postGraphQL<{ hub: { agent: { db: { agent_types: RawAgentType[] } } } }>(
        `query { hub { agent { db { agent_types { id name description config } } } } }`,
      )
      return d.hub.agent.db.agent_types
        .map<AgentType>((t) => ({
          id: t.id,
          name: t.name,
          description: t.description ?? '',
          config: jsonText(t.config),
        }))
        .sort((a, b) => a.name.localeCompare(b.name))
    },
  )
}

/* ── mutations ────────────────────────────────────────────────────────── */

export async function createAgentType(input: AgentTypeInput): Promise<void> {
  return withDemo(
    () => {
      demoTypes = [
        ...demoTypes,
        { id: input.id, name: input.name, description: input.description ?? '', config: input.config },
      ]
    },
    async () => {
      // insert returns the row (PK table).
      await postGraphQL(
        `mutation ($data: hub_agent_db_agent_types_mut_input_data!) {
          hub { agent { db { insert_agent_types(data: $data) { id } } } }
        }`,
        {
          data: {
            id: input.id,
            name: input.name,
            description: input.description ?? '',
            config: parseConfig(input.config),
          },
        },
      )
    },
  )
}

export async function updateAgentType(
  id: string,
  patch: { name?: string; description?: string; config?: string },
): Promise<void> {
  return withDemo(
    () => {
      demoTypes = demoTypes.map((t) =>
        t.id === id
          ? {
              ...t,
              name: patch.name ?? t.name,
              description: patch.description ?? t.description,
              config: patch.config ?? t.config,
            }
          : t,
      )
    },
    async () => {
      const data: Record<string, unknown> = {}
      if (patch.name !== undefined) data.name = patch.name
      if (patch.description !== undefined) data.description = patch.description
      if (patch.config !== undefined) data.config = parseConfig(patch.config)
      // update returns OperationResult.
      await postGraphQL(
        `mutation ($filter: hub_agent_db_agent_types_filter!, $data: hub_agent_db_agent_types_mut_data!) {
          hub { agent { db { update_agent_types(filter: $filter, data: $data) { success message } } } }
        }`,
        { filter: { id: { eq: id } }, data },
      )
    },
  )
}

export async function deleteAgentType(id: string): Promise<void> {
  return withDemo(
    () => {
      demoTypes = demoTypes.filter((t) => t.id !== id)
    },
    async () => {
      await postGraphQL(
        `mutation ($filter: hub_agent_db_agent_types_filter!) {
          hub { agent { db { delete_agent_types(filter: $filter) { success message } } } }
        }`,
        { filter: { id: { eq: id } } },
      )
    },
  )
}

/** Duplicate a type into a new id (config + description carried verbatim). */
export async function copyAgentType(source: AgentType, newId: string, newName: string): Promise<void> {
  return createAgentType({
    id: newId,
    name: newName,
    description: source.description,
    config: source.config,
  })
}
