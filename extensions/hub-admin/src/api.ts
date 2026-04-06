/**
 * Hugr GraphQL API for hub.* schema queries.
 * Uses HugrClient with hugr_connection_service proxy (auth handled server-side).
 * Field names must match hub.graphql SDL exactly.
 */

import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';
import { HugrClient } from './hugrClient.js';

// ── Types matching hub.graphql SDL ───────────────────────────────

export interface AgentInstance {
  id: string;
  user_id: string;
  agent_type_id: string;
  status: string;
  container_id: string;
  started_at: string;
  last_activity_at: string;
}

export interface AgentSession {
  id: string;
  user_id: string;
  instance_id: string;
  started_at: string;
  ended_at: string;
}

export interface LLMProvider {
  id: string;
  provider: string;
  model: string;
  base_url: string;
  max_tokens_per_request: number;
  enabled: boolean;
}

export interface LLMBudget {
  id: string;
  scope: string;
  provider_id: string;
  period: string;
  max_tokens_in: number;
  max_tokens_out: number;
  max_requests: number;
}

export interface LLMUsage {
  id: string;
  user_id: string;
  provider_id: string;
  tokens_in: number;
  tokens_out: number;
  duration_ms: number;
  created_at: string;
}

// ── Client singleton ─────────────────────────────────────────────

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

  const connections: Array<{ name: string; status?: string }> = await resp.json();
  const defaultConn = connections.find(c => c.status === 'default') || connections[0];
  if (!defaultConn) {
    throw new Error('No Hugr connection configured');
  }

  _client = new HugrClient({
    url: baseUrl + 'hugr/proxy/' + encodeURIComponent(defaultConn.name),
    authType: 'public',
    connectionName: defaultConn.name,
  });

  return _client;
}

async function hugrQuery(query: string, variables?: Record<string, unknown>): Promise<any> {
  const client = await getClient();
  const result = await client.query(query, variables);

  if (result.errors.length > 0) {
    throw new Error(result.errors.map(e => e.message).join(', '));
  }

  return result.data;
}

// ── Queries ──────────────────────────────────────────────────────

export async function fetchAgentInstances(): Promise<AgentInstance[]> {
  const data = await hugrQuery(`{
    hub { db { agent_instances(order_by: [{field: "started_at", direction: DESC}], limit: 50) {
      id user_id agent_type_id status container_id started_at last_activity_at
    } } }
  }`);
  return data?.hub?.db?.agent_instances ?? [];
}

export async function fetchAgentSessions(): Promise<AgentSession[]> {
  const data = await hugrQuery(`{
    hub { db { agent_sessions(order_by: [{field: "started_at", direction: DESC}], limit: 50) {
      id user_id instance_id started_at ended_at
    } } }
  }`);
  return data?.hub?.db?.agent_sessions ?? [];
}

export async function fetchLLMProviders(): Promise<LLMProvider[]> {
  const data = await hugrQuery(`{
    hub { db { llm_providers(order_by: [{field: "id", direction: ASC}]) {
      id provider model base_url max_tokens_per_request enabled
    } } }
  }`);
  return data?.hub?.db?.llm_providers ?? [];
}

export async function fetchLLMBudgets(): Promise<LLMBudget[]> {
  const data = await hugrQuery(`{
    hub { db { llm_budgets(order_by: [{field: "scope", direction: ASC}]) {
      id scope provider_id period max_tokens_in max_tokens_out max_requests
    } } }
  }`);
  return data?.hub?.db?.llm_budgets ?? [];
}

export async function fetchLLMUsage(limit = 100): Promise<LLMUsage[]> {
  const data = await hugrQuery(
    `query($limit: Int!) {
      hub { db { llm_usage(order_by: [{field: "created_at", direction: DESC}], limit: $limit) {
        id user_id provider_id tokens_in tokens_out duration_ms created_at
      } } }
    }`,
    { limit },
  );
  return data?.hub?.db?.llm_usage ?? [];
}

// ── Mutations ────────────────────────────────────────────────────

export async function insertLLMProvider(
  provider: Omit<LLMProvider, 'id'> & { id: string },
): Promise<string> {
  const data = await hugrQuery(
    `mutation($data: hub_db_llm_providers_mut_input_data!) {
      hub { db { insert_llm_providers(data: $data) { id } } }
    }`,
    {
      data: {
        id: provider.id,
        provider: provider.provider,
        model: provider.model,
        base_url: provider.base_url,
        enabled: provider.enabled,
      },
    },
  );
  return data?.hub?.db?.insert_llm_providers?.id;
}

export async function updateLLMProvider(
  id: string,
  updates: Partial<Omit<LLMProvider, 'id'>>,
): Promise<void> {
  const updateData: Record<string, unknown> = {};
  if (updates.enabled !== undefined) updateData.enabled = updates.enabled;
  if (updates.base_url !== undefined) updateData.base_url = updates.base_url;
  if (updates.model !== undefined) updateData.model = updates.model;

  if (Object.keys(updateData).length === 0) return;

  await hugrQuery(
    `mutation($filter: hub_db_llm_providers_filter!, $data: hub_db_llm_providers_mut_data!) {
      hub { db { update_llm_providers(filter: $filter, data: $data) { affected_rows } } }
    }`,
    {
      filter: { id: { eq: id } },
      data: updateData,
    },
  );
}

export async function insertLLMBudget(
  budget: Omit<LLMBudget, 'id'>,
): Promise<string> {
  const data = await hugrQuery(
    `mutation($data: hub_db_llm_budgets_mut_input_data!) {
      hub { db { insert_llm_budgets(data: $data) { id } } }
    }`,
    {
      data: {
        scope: budget.scope,
        provider_id: budget.provider_id || null,
        period: budget.period,
        max_tokens_in: budget.max_tokens_in,
        max_tokens_out: budget.max_tokens_out,
        max_requests: budget.max_requests,
      },
    },
  );
  return data?.hub?.db?.insert_llm_budgets?.id;
}

export async function deleteLLMBudget(id: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: hub_db_llm_budgets_filter!) {
      hub { db { delete_llm_budgets(filter: $filter) { affected_rows } } }
    }`,
    { filter: { id: { eq: id } } },
  );
}

export async function stopAgent(instanceId: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: hub_db_agent_instances_filter!, $data: hub_db_agent_instances_mut_data!) {
      hub { db { update_agent_instances(filter: $filter, data: $data) { affected_rows } } }
    }`,
    {
      filter: { id: { eq: instanceId } },
      data: { status: 'stopped' },
    },
  );
}

export async function clearAgentMemory(userId: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: hub_db_agent_memory_filter!) {
      hub { db { delete_agent_memory(filter: $filter) { affected_rows } } }
    }`,
    { filter: { user_id: { eq: userId } } },
  );
}
