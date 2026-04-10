/**
 * Agent operations via Hugr GraphQL.
 *
 * All admin/CRUD on agents goes through this module — no REST proxy.
 * - Standard CRUD (`createAgent`, `renameAgent`, `fetchAgents`) uses Hugr's
 *   auto-generated mutations on hub.db.agents and hub.db.user_agents.
 * - Lifecycle actions (`startAgent`, `stopAgent`, `deleteAgent`) use airport-go
 *   mutating functions registered in pkg/hubapp/handlers_agent.go. Identity is
 *   auto-injected by the Hugr planner via @arg_default placeholders.
 *
 * Backend reference: pkg/hubapp/README.md
 * Migration history:  extensions/hub-admin/MIGRATION.md
 */

import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';
import { HugrClient } from './hugrClient.js';

// ── Types ─────────────────────────────────────────────────

/** Agent identity (from hub.db.agents) merged with optional runtime state (from hub.agent_runtime). */
export interface Agent {
  id: string;
  agent_type_id: string;
  display_name: string;
  description: string | null;
  hugr_user_id: string;
  hugr_user_name: string;
  hugr_role: string;
  created_at: string;
  last_activity_at: string | null;
  // Runtime state — merged from hub.agent_runtime; absent when agent is stopped.
  container_id?: string;
  status?: string;
  started_at?: string;
}

export interface CreateAgentInput {
  agent_type_id: string;
  display_name?: string;
  description?: string;
  /** For team agents — set explicit Hugr identity. Empty = personal (inherits owner). */
  hugr_user_id?: string;
  hugr_user_name?: string;
  hugr_role?: string;
  /** Owner — defaults to caller if not set. */
  owner_user_id?: string;
}

export interface CreatedAgent {
  id: string;
  display_name: string;
  agent_type_id: string;
  hugr_user_id: string;
  hugr_role: string;
}

// ── Client (lazy) ─────────────────────────────────────────

let _client: HugrClient | null = null;

async function getClient(): Promise<HugrClient> {
  if (_client) return _client;

  const baseUrl = PageConfig.getBaseUrl();
  const settings = ServerConnection.makeSettings();

  const resp = await ServerConnection.makeRequest(
    baseUrl + 'hugr/connections',
    {},
    settings,
  );
  if (!resp.ok) {
    throw new Error(`Failed to get connections: ${resp.status}`);
  }

  const connections: Array<{ name: string; status?: string; query_timeout?: number }> = await resp.json();
  const defaultConn = connections.find(c => c.status === 'default') || connections[0];
  if (!defaultConn) {
    throw new Error('No Hugr connection configured');
  }

  _client = new HugrClient({
    url: baseUrl + 'hugr/proxy/' + encodeURIComponent(defaultConn.name),
    authType: 'public',
    connectionName: defaultConn.name,
    timeout: defaultConn.query_timeout ? defaultConn.query_timeout * 1000 : undefined,
  });

  return _client;
}

async function gqlQuery(query: string, variables?: Record<string, unknown>): Promise<any> {
  const client = await getClient();
  const result = await client.query(query, variables);
  if (result.errors.length > 0) {
    throw new Error(result.errors.map(e => e.message).join(', '));
  }
  return result.data;
}

// ── Agent CRUD via standard Hugr mutations ────────────────

/**
 * Create an agent identity + grant owner access in one atomic Hugr mutation.
 *
 * For personal agents, leave `hugr_user_id` empty — the caller (or
 * `owner_user_id`) becomes the Hugr identity.
 * For team agents, pass explicit `hugr_user_id` + `hugr_role`.
 */
export async function createAgent(input: CreateAgentInput): Promise<CreatedAgent> {
  const id = `agent-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const displayName = input.display_name || input.agent_type_id;
  const description = input.description || '';
  const ownerID = input.owner_user_id || '';
  // Personal vs team identity
  const hugrUserID = input.hugr_user_id || ownerID;
  const hugrUserName = input.hugr_user_name || hugrUserID;
  const hugrRole = input.hugr_role || 'agent';

  const data = await gqlQuery(
    `mutation(
      $id: String!, $tid: String!, $name: String!, $desc: String,
      $huid: String!, $hun: String!, $hr: String!,
      $owner: String!
    ) {
      hub { db {
        insert_agents(data: {
          id: $id
          agent_type_id: $tid
          display_name: $name
          description: $desc
          hugr_user_id: $huid
          hugr_user_name: $hun
          hugr_role: $hr
        }) { id display_name agent_type_id hugr_user_id hugr_role }
        insert_user_agents(data: {
          user_id: $owner
          agent_id: $id
          role: "owner"
        }) { user_id }
      } }
    }`,
    {
      id, tid: input.agent_type_id, name: displayName, desc: description,
      huid: hugrUserID, hun: hugrUserName, hr: hugrRole,
      owner: ownerID,
    },
  );

  const inserted = data?.hub?.db?.insert_agents;
  if (!inserted) {
    throw new Error('insert_agents returned no data');
  }
  return inserted as CreatedAgent;
}

/** Rename an agent — standard Hugr update_agents mutation. */
export async function renameAgent(agentId: string, displayName: string): Promise<void> {
  await gqlQuery(
    `mutation($id: String!, $name: String!) {
      hub { db { update_agents(
        filter: { id: { eq: $id } }
        data: { display_name: $name }
      ) { affected_rows } } }
    }`,
    { id: agentId, name: displayName },
  );
}

// ── Agent runtime via airport-go mutating functions ───────

/**
 * Start an agent container. Caller must have owner access (or be admin).
 * Returns the short Docker container ID.
 *
 * Backend: pkg/hubapp/handlers_agent.go handleStartAgent
 */
export async function startAgent(agentId: string): Promise<string> {
  const data = await gqlQuery(
    `mutation($id: String!) {
      function { hub { start_agent(agent_id: $id) } }
    }`,
    { id: agentId },
  );
  return data?.function?.hub?.start_agent ?? '';
}

/**
 * Stop a running agent. Idempotent — succeeds even if not running.
 *
 * Backend: pkg/hubapp/handlers_agent.go handleStopAgent
 */
export async function stopAgent(agentId: string): Promise<string> {
  const data = await gqlQuery(
    `mutation($id: String!) {
      function { hub { stop_agent(agent_id: $id) } }
    }`,
    { id: agentId },
  );
  return data?.function?.hub?.stop_agent ?? '';
}

/**
 * Stop the agent (if running) and delete identity + access grants. Admin only.
 * Idempotent — succeeds even if already deleted.
 *
 * Backend: pkg/hubapp/handlers_agent.go handleDeleteAgent
 */
export async function deleteAgent(agentId: string): Promise<string> {
  const data = await gqlQuery(
    `mutation($id: String!) {
      function { hub { delete_agent(agent_id: $id) } }
    }`,
    { id: agentId },
  );
  return data?.function?.hub?.delete_agent ?? '';
}

// ── Read operations ───────────────────────────────────────

/**
 * Fetch all agents with merged runtime state.
 * Identity comes from hub.db.agents (persistent).
 * Runtime comes from hub.agent_runtime (in-memory, only running agents present).
 */
export async function fetchAgents(): Promise<Agent[]> {
  const [identityRes, runtimeRes] = await Promise.all([
    gqlQuery(`{
      hub { db { agents(order_by: [{field: "created_at", direction: DESC}], limit: 100) {
        id agent_type_id display_name description hugr_user_id hugr_user_name hugr_role
        created_at last_activity_at
      } } }
    }`),
    gqlQuery(`{
      hub { agent_runtime(args: { agent_id: "" }) {
        agent_id container_id status started_at
      } }
    }`).catch(() => ({ hub: { agent_runtime: [] } })),
  ]);

  const identities: Agent[] = identityRes?.hub?.db?.agents ?? [];
  const runtimes: Array<{ agent_id: string; container_id: string; status: string; started_at: string }> =
    runtimeRes?.hub?.agent_runtime ?? [];
  const runtimeMap = new Map(runtimes.map(r => [r.agent_id, r]));

  return identities.map(a => {
    const rt = runtimeMap.get(a.id);
    return rt
      ? { ...a, container_id: rt.container_id, status: rt.status, started_at: rt.started_at }
      : { ...a, status: 'stopped' };
  });
}

/**
 * Fetch the agents the current user has access to (from user_agents grants).
 * Replaces REST GET /api/user/agents.
 *
 * Note: Hugr filters rows via auth context — admins see everything, regular
 * users see only their own grants based on RBAC on hub.db.user_agents.
 */
export async function fetchUserAgents(): Promise<Array<{
  id: string;
  agent_type_id: string;
  display_name: string;
  role: string;
  status: string;
}>> {
  const data = await gqlQuery(`{
    hub { db { user_agents {
      role
      agent { id display_name agent_type_id hugr_role }
    } } }
  }`);
  const access = data?.hub?.db?.user_agents ?? [];

  // Optionally enrich with runtime status
  const runtimeRes = await gqlQuery(`{
    hub { agent_runtime(args: { agent_id: "" }) { agent_id status }}
  }`).catch(() => ({ hub: { agent_runtime: [] } }));
  const runtimes: Array<{ agent_id: string; status: string }> = runtimeRes?.hub?.agent_runtime ?? [];
  const statusMap = new Map(runtimes.map(r => [r.agent_id, r.status]));

  return access.map((ua: any) => ({
    id: ua.agent?.id ?? '',
    agent_type_id: ua.agent?.agent_type_id ?? '',
    display_name: ua.agent?.display_name ?? '',
    role: ua.role,
    status: statusMap.get(ua.agent?.id ?? '') ?? 'stopped',
  }));
}
