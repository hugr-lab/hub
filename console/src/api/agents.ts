import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

/* ────────────────────────────────────────────────────────────────────────
 * Agents — namespace `hub`
 *
 * Fleet + lifecycle + provisioning + access grants for the hub's fenced AI
 * agents. Reads come from the `hub.my_agent_instances` table function (identity
 * + live container status) and the `hub.agent_access` grant function; mutations
 * are `function.hub.*` calls re-checked per invocation on the server.
 *
 * NOTE on the wire contract (verified against pkg/hubapp): the live
 * `my_agent_instances` view is intentionally lean — it exposes only
 * `id / agent_type_id / display_name / hugr_role / status / access_role`. The
 * richer per-agent facts the fleet UI shows (owner, sessions, model, version,
 * created_at, config_override, and a separate desired vs. runtime status) are
 * NOT on that view today; they are populated in demo mode and default
 * gracefully in live mode. See the mapping in `listAgents` and the TODOs in the
 * screen report.
 * ──────────────────────────────────────────────────────────────────────── */

/** Operator intent — what the fleet controller should converge the agent to. */
export type DesiredStatus = 'active' | 'manual' | 'paused' | 'disabled'
/** Live container state reported by the runtime. */
export type RuntimeStatus = 'running' | 'starting' | 'stopped' | 'error'
/** Per-agent access grant level (`owner` ⊃ `member`). */
export type AccessRole = 'member' | 'owner'

export interface Agent {
  /** Agent id (stable handle, e.g. `agt_7f3a`) — the view's `id`. */
  id: string
  agent_id: string
  /** The view's `display_name`. */
  name: string
  /** Floored hugr data role the agent runs under. */
  hugr_role: string
  agent_type_id: string
  owner: string
  sessions: number
  desired_status: DesiredStatus
  runtime_status: RuntimeStatus
  connected: boolean
  model: string
  version: string
  created_at: string
  /** Per-agent JSON merged over the fleet default at converge time. */
  config_override: string
}

export interface AgentAccess {
  agent_id: string
  /** The grantee (the view's `user_id_grant`). */
  user_id: string
  /** Human-readable name, falls back to the id for stub grants. */
  user_name: string
  access_role: AccessRole
}

export interface CreateAgentInput {
  name: string
  /** '' → backend auto-creates a floored `agent:<id>` role. */
  hugr_role: string
  /** JSON string; blank → `{}`. */
  config_override?: string
  /** Owner grantee; '' → skip the owner grant. */
  owner_user_id?: string
  /** Fleet template; defaults to `DEFAULT_AGENT_TYPE`. */
  agent_type_id?: string
}

export interface CreateAgentResult {
  agent_id: string
  status: string
  /** One-time bootstrap secret — shown once, never retrievable again. */
  secret: string
  expires_at: string
}

export interface UpdateAgentInput {
  name?: string
  hugr_role?: string
  status?: DesiredStatus
  config_override?: string
}

/** Fallback fleet template id used when the wizard doesn't collect one. */
const DEFAULT_AGENT_TYPE = 'hugen'

/* ── demo store ───────────────────────────────────────────────────────────
 * A mutable in-memory fleet so `?demo=1` is fully interactive: lifecycle and
 * grant mutations mutate these, and the next query re-reads them (fetchers use
 * the function form of `withDemo` so they observe the latest state).
 * ──────────────────────────────────────────────────────────────────────── */

let demoAgents: Agent[] = [
  {
    id: 'agt_7f3a',
    agent_id: 'agt_7f3a',
    name: 'analytics-copilot',
    hugr_role: 'agent:analytics',
    agent_type_id: 'hugen',
    owner: 'm.keller',
    sessions: 3,
    desired_status: 'active',
    runtime_status: 'running',
    connected: true,
    model: 'claude-sonnet-4',
    version: 'hugen 0.9.4',
    created_at: '2026-05-02',
    config_override:
      '{\n  "max_turns": 40,\n  "tools": { "hugr_query": { "row_limit": 5000 } },\n  "skills": ["sql-analyst", "report-writer"]\n}',
  },
  {
    id: 'agt_2c91',
    agent_id: 'agt_2c91',
    name: 'geo-research',
    hugr_role: 'agent:geo',
    agent_type_id: 'hugen',
    owner: 'a.novak',
    sessions: 1,
    desired_status: 'active',
    runtime_status: 'running',
    connected: true,
    model: 'claude-sonnet-4',
    version: 'hugen 0.9.4',
    created_at: '2026-05-19',
    config_override: '{\n  "skills": ["geo-tools"]\n}',
  },
  {
    id: 'agt_4b77',
    agent_id: 'agt_4b77',
    name: 'finance-qa',
    hugr_role: 'agent:finance',
    agent_type_id: 'hugen',
    owner: 'j.ortiz',
    sessions: 0,
    desired_status: 'active',
    runtime_status: 'starting',
    connected: false,
    model: 'claude-sonnet-4',
    version: 'hugen 0.9.4',
    created_at: '2026-06-27',
    config_override: '{\n  "budget": "finance-q3"\n}',
  },
  {
    id: 'agt_9d10',
    agent_id: 'agt_9d10',
    name: 'etl-warden',
    hugr_role: 'agent:etl',
    agent_type_id: 'hugen',
    owner: 'm.keller',
    sessions: 0,
    desired_status: 'paused',
    runtime_status: 'stopped',
    connected: false,
    model: 'claude-haiku-4',
    version: 'hugen 0.9.2',
    created_at: '2026-04-11',
    config_override: '{}',
  },
  {
    id: 'agt_1a55',
    agent_id: 'agt_1a55',
    name: 'billing-reconciler',
    hugr_role: 'agent:billing',
    agent_type_id: 'hugen',
    owner: 'j.ortiz',
    sessions: 0,
    desired_status: 'active',
    runtime_status: 'error',
    connected: false,
    model: 'claude-sonnet-4',
    version: 'hugen 0.9.4',
    created_at: '2026-06-02',
    config_override: '{\n  "budget": "billing-q3"\n}',
  },
  {
    id: 'agt_0e42',
    agent_id: 'agt_0e42',
    name: 'support-triage',
    hugr_role: 'agent:support',
    agent_type_id: 'hugen',
    owner: 's.lindqvist',
    sessions: 0,
    desired_status: 'disabled',
    runtime_status: 'stopped',
    connected: false,
    model: 'claude-haiku-4',
    version: 'hugen 0.8.9',
    created_at: '2026-03-30',
    config_override: '{}',
  },
]

const demoAccess: Record<string, AgentAccess[]> = {
  agt_7f3a: [
    { agent_id: 'agt_7f3a', user_id: 'm.keller', user_name: 'Maren Keller', access_role: 'owner' },
    { agent_id: 'agt_7f3a', user_id: 'j.ortiz', user_name: 'Julia Ortiz', access_role: 'member' },
    { agent_id: 'agt_7f3a', user_id: 'a.novak', user_name: 'Adam Novak', access_role: 'member' },
  ],
  agt_2c91: [{ agent_id: 'agt_2c91', user_id: 'a.novak', user_name: 'Adam Novak', access_role: 'owner' }],
  agt_4b77: [
    { agent_id: 'agt_4b77', user_id: 'j.ortiz', user_name: 'Julia Ortiz', access_role: 'owner' },
    { agent_id: 'agt_4b77', user_id: 'm.keller', user_name: 'Maren Keller', access_role: 'member' },
  ],
  agt_9d10: [{ agent_id: 'agt_9d10', user_id: 'm.keller', user_name: 'Maren Keller', access_role: 'owner' }],
  agt_1a55: [{ agent_id: 'agt_1a55', user_id: 'j.ortiz', user_name: 'Julia Ortiz', access_role: 'owner' }],
  agt_0e42: [
    { agent_id: 'agt_0e42', user_id: 's.lindqvist', user_name: 'Sven Lindqvist', access_role: 'owner' },
  ],
}

function demoPatch(agentId: string, patch: Partial<Agent>): void {
  demoAgents = demoAgents.map((a) => (a.id === agentId ? { ...a, ...patch } : a))
}

/* ── status mapping (live view → rich UI enums) ───────────────────────── */

/** Normalize the view's single `status` into a runtime enum. */
function coerceRuntime(status: string): RuntimeStatus {
  switch (status) {
    case 'running':
    case 'active':
    case 'connected':
      return 'running'
    case 'starting':
    case 'pending':
      return 'starting'
    case 'error':
    case 'failed':
    case 'crashed':
      return 'error'
    default:
      return 'stopped'
  }
}

/** Best-effort desired status from the view's single `status`. */
function coerceDesired(status: string): DesiredStatus {
  switch (status) {
    case 'active':
    case 'running':
    case 'starting':
      return 'active'
    case 'manual':
      return 'manual'
    case 'disabled':
      return 'disabled'
    default:
      return 'paused'
  }
}

/* ── reads ────────────────────────────────────────────────────────────── */

interface AgentInstanceRow {
  id: string
  agent_type_id: string
  display_name: string
  hugr_role: string
  status: string
  access_role: string
}

/**
 * Fleet the caller can see (`hub.my_agent_instances`) — identity + live
 * container status. Admins see the whole fleet; owners see only granted agents
 * (the server scopes the view). Cached under `['agents']` so the sidebar badge
 * can read its length.
 */
export async function listAgents(): Promise<Agent[]> {
  return withDemo(
    () => demoAgents.slice(),
    async () => {
      const d = await postGraphQL<{ hub: { my_agent_instances: AgentInstanceRow[] } }>(
        `query Agents {
          hub {
            my_agent_instances { id agent_type_id display_name hugr_role status access_role }
          }
        }`,
      )
      return d.hub.my_agent_instances.map<Agent>((r) => ({
        id: r.id,
        agent_id: r.id,
        name: r.display_name,
        hugr_role: r.hugr_role,
        agent_type_id: r.agent_type_id,
        owner: '',
        sessions: 0,
        desired_status: coerceDesired(r.status),
        runtime_status: coerceRuntime(r.status),
        connected: coerceRuntime(r.status) === 'running',
        model: '',
        version: '',
        created_at: '',
        config_override: '',
      }))
    },
  )
}

interface AgentAccessRow {
  user_id_grant: string
  user_name: string
  access_role: AccessRole
  created_at: string
}

/** Access grants for one agent (`hub.agent_access`, admin-only). */
export async function listAgentAccess(agentId: string): Promise<AgentAccess[]> {
  return withDemo(
    () => (demoAccess[agentId] ?? []).slice(),
    async () => {
      const d = await postGraphQL<{ hub: { agent_access: AgentAccessRow[] } }>(
        `query AgentAccess($args: hub_agent_access_args!) {
          hub { agent_access(args: $args) { user_id_grant user_name access_role created_at } }
        }`,
        { args: { agent_id: agentId } },
      )
      return d.hub.agent_access.map<AgentAccess>((r) => ({
        agent_id: agentId,
        user_id: r.user_id_grant,
        user_name: r.user_name || r.user_id_grant,
        access_role: r.access_role,
      }))
    },
  )
}

/* ── mutation helper ──────────────────────────────────────────────────── */

async function mutateHub<T>(query: string, variables: Record<string, unknown>): Promise<T> {
  const d = await postGraphQL<{ function: { hub: T } }>(query, variables)
  return d.function.hub
}

/* ── lifecycle (admin) ────────────────────────────────────────────────── */

/** Desired active + converge. `start_agent → agent_runtime_state{id,status}`. */
export async function startAgent(agentId: string): Promise<void> {
  return withDemo(
    () => {
      demoPatch(agentId, { desired_status: 'active', runtime_status: 'running', connected: true })
    },
    async () => {
      await mutateHub(
        `mutation StartAgent($id: String!) {
          function { hub { start_agent(agent_id: $id) { id status } } }
        }`,
        { id: agentId },
      )
    },
  )
}

/** Desired paused. `stop_agent → agent_runtime_state{id,status}`. */
export async function stopAgent(agentId: string): Promise<void> {
  return withDemo(
    () => {
      demoPatch(agentId, {
        desired_status: 'paused',
        runtime_status: 'stopped',
        connected: false,
        sessions: 0,
      })
    },
    async () => {
      await mutateHub(
        `mutation StopAgent($id: String!) {
          function { hub { stop_agent(agent_id: $id) { id status } } }
        }`,
        { id: agentId },
      )
    },
  )
}

/** Admin — desired disabled. `disable_agent → agent_identity{id,status}`. */
export async function disableAgent(agentId: string): Promise<void> {
  return withDemo(
    () => {
      demoPatch(agentId, {
        desired_status: 'disabled',
        runtime_status: 'stopped',
        connected: false,
        sessions: 0,
      })
    },
    async () => {
      await mutateHub(
        `mutation DisableAgent($id: String!) {
          function { hub { disable_agent(agent_id: $id) { id status } } }
        }`,
        { id: agentId },
      )
    },
  )
}

/** Admin — destructive. `delete_agent → String` (the deleted id). */
export async function deleteAgent(agentId: string): Promise<void> {
  return withDemo(
    () => {
      demoAgents = demoAgents.filter((a) => a.id !== agentId)
      delete demoAccess[agentId]
    },
    async () => {
      await mutateHub(
        `mutation DeleteAgent($id: String!) { function { hub { delete_agent(agent_id: $id) } } }`,
        { id: agentId },
      )
    },
  )
}

/* ── access grants (admin) ────────────────────────────────────────────── */

export async function grantAgentAccess(
  agentId: string,
  userId: string,
  accessRole: AccessRole,
): Promise<void> {
  return withDemo(
    () => {
      const list = demoAccess[agentId] ?? (demoAccess[agentId] = [])
      const existing = list.find((g) => g.user_id === userId)
      if (existing) existing.access_role = accessRole
      else list.push({ agent_id: agentId, user_id: userId, user_name: userId, access_role: accessRole })
    },
    async () => {
      await mutateHub(
        `mutation GrantAgentAccess($user: String!, $id: String!, $role: String!) {
          function {
            hub {
              grant_agent_access(user_id_grant: $user, agent_id: $id, access_role: $role) {
                user_id agent_id access_role
              }
            }
          }
        }`,
        { user: userId, id: agentId, role: accessRole },
      )
    },
  )
}

export async function revokeAgentAccess(agentId: string, userId: string): Promise<void> {
  return withDemo(
    () => {
      demoAccess[agentId] = (demoAccess[agentId] ?? []).filter((g) => g.user_id !== userId)
    },
    async () => {
      await mutateHub(
        `mutation RevokeAgentAccess($user: String!, $id: String!) {
          function { hub { revoke_agent_access(user_id_grant: $user, agent_id: $id) { id deleted } } }
        }`,
        { user: userId, id: agentId },
      )
    },
  )
}

/* ── provisioning (admin) ─────────────────────────────────────────────── */

/**
 * Create a floored hugr role usable as an agent's `hugr_role` — deny-all floor,
 * then allow per source. `create_agent_role → agent_role{role,status}`.
 */
export async function createAgentRole(role: string): Promise<string> {
  return withDemo(
    () => role,
    async () => {
      const d = await mutateHub<{ create_agent_role: { role: string } }>(
        `mutation CreateAgentRole($role: String!) {
          function { hub { create_agent_role(hugr_role: $role) { role status } } }
        }`,
        { role },
      )
      return d.create_agent_role.role
    },
  )
}

/** Slugify a display name into a usable agent id / role fragment. */
function slugify(name: string): string {
  return (
    name
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'agent'
  )
}

/**
 * Provision an agent. Returns the one-time bootstrap secret the container uses
 * to register with the hub (shown once, never retrievable again).
 * `create_agent → agent_provision{agent_id,status,secret,expires_at}`.
 */
export async function createAgent(input: CreateAgentInput): Promise<CreateAgentResult> {
  const override = input.config_override?.trim() || '{}'
  return withDemo(
    () => {
      const id = 'agt_' + cryptoToken(4)
      demoAgents = [
        ...demoAgents,
        {
          id,
          agent_id: id,
          name: input.name,
          hugr_role: input.hugr_role || `agent:${slugify(input.name)}`,
          agent_type_id: input.agent_type_id ?? DEFAULT_AGENT_TYPE,
          owner: input.owner_user_id || 'm.keller',
          sessions: 0,
          desired_status: 'manual',
          runtime_status: 'stopped',
          connected: false,
          model: 'claude-sonnet-4',
          version: 'hugen 0.9.4',
          created_at: new Date().toISOString().slice(0, 10),
          config_override: override,
        },
      ]
      demoAccess[id] = input.owner_user_id
        ? [{ agent_id: id, user_id: input.owner_user_id, user_name: input.owner_user_id, access_role: 'owner' }]
        : []
      const expires = new Date(Date.now() + 24 * 3600_000).toISOString().slice(0, 16) + 'Z'
      return {
        agent_id: id,
        status: 'manual',
        secret: 'bsk_' + cryptoToken(40),
        expires_at: expires,
      }
    },
    async () => {
      const d = await mutateHub<{ create_agent: CreateAgentResult }>(
        `mutation CreateAgent(
          $id: String!, $type: String!, $name: String!,
          $role: String!, $owner: String!, $override: JSON!
        ) {
          function {
            hub {
              create_agent(
                agent_id: $id, agent_type_id: $type, name: $name,
                hugr_role: $role, owner_user_id: $owner, config_override: $override
              ) { agent_id status secret expires_at }
            }
          }
        }`,
        {
          id: slugify(input.name),
          type: input.agent_type_id ?? DEFAULT_AGENT_TYPE,
          name: input.name,
          role: input.hugr_role,
          owner: input.owner_user_id ?? '',
          override: JSON.parse(override),
        },
      )
      return d.create_agent
    },
  )
}

/**
 * Update an agent's identity/desired-state/config. Partial: an omitted field is
 * sent as '' / `{}`, which the backend treats as "unchanged".
 * `update_agent → agent_identity{id,status}`.
 */
export async function updateAgent(agentId: string, input: UpdateAgentInput): Promise<void> {
  return withDemo(
    () => {
      const patch: Partial<Agent> = {}
      if (input.name !== undefined) patch.name = input.name
      if (input.hugr_role !== undefined) patch.hugr_role = input.hugr_role
      if (input.status !== undefined) patch.desired_status = input.status
      if (input.config_override !== undefined) patch.config_override = input.config_override
      demoPatch(agentId, patch)
    },
    async () => {
      const overrideProvided = input.config_override !== undefined && input.config_override.trim() !== ''
      await mutateHub(
        `mutation UpdateAgent(
          $id: String!, $name: String!, $role: String!, $status: String!, $override: JSON!
        ) {
          function {
            hub {
              update_agent(
                agent_id: $id, name: $name, hugr_role: $role,
                status: $status, config_override: $override
              ) { id status }
            }
          }
        }`,
        {
          id: agentId,
          name: input.name ?? '',
          role: input.hugr_role ?? '',
          status: input.status ?? '',
          override: overrideProvided ? JSON.parse(input.config_override as string) : {},
        },
      )
    },
  )
}

/** Random hex token for the demo bootstrap secret / id suffix. */
function cryptoToken(len: number): string {
  if (typeof crypto !== 'undefined' && 'getRandomValues' in crypto) {
    const bytes = new Uint8Array(Math.ceil(len / 2))
    crypto.getRandomValues(bytes)
    return Array.from(bytes, (b) => b.toString(16).padStart(2, '0'))
      .join('')
      .slice(0, len)
  }
  let out = ''
  while (out.length < len) out += Math.random().toString(16).slice(2)
  return out.slice(0, len)
}
