/**
 * Hugr GraphQL client for hub.* schema queries.
 * Uses the workspace's Hugr connection (HUGR_URL from environment).
 */

import { ServerConnection } from '@jupyterlab/services';

const HUB_GQL_PATH = '/hugr/graphql';

export interface AgentInstance {
  id: string;
  user_id: string;
  agent_type_id: string;
  status: string;
  container_id: string;
  started_at: string;
  stopped_at: string;
}

export interface AgentSession {
  id: string;
  user_id: string;
  agent_instance_id: string;
  started_at: string;
  ended_at: string;
  message_count: number;
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
  scope_id: string;
  period: string;
  max_tokens: number;
  max_requests: number;
}

export interface LLMUsage {
  id: string;
  user_id: string;
  provider_id: string;
  input_tokens: number;
  output_tokens: number;
  created_at: string;
}

async function hugrQuery(query: string, variables?: Record<string, unknown>): Promise<any> {
  const settings = ServerConnection.makeSettings();
  const url = settings.baseUrl + HUB_GQL_PATH;

  const response = await ServerConnection.makeRequest(
    url,
    {
      method: 'POST',
      body: JSON.stringify({ query, variables }),
    },
    settings,
  );

  if (!response.ok) {
    throw new Error(`Hugr query failed: ${response.status} ${response.statusText}`);
  }

  const json = await response.json();
  if (json.errors) {
    throw new Error(json.errors.map((e: any) => e.message).join(', '));
  }
  return json.data;
}

export async function fetchAgentInstances(): Promise<AgentInstance[]> {
  const data = await hugrQuery(`{
    hub { hub { agent_instances(order_by: { started_at: desc }, limit: 50) {
      id user_id agent_type_id status container_id started_at stopped_at
    } } }
  }`);
  return data?.hub?.hub?.agent_instances ?? [];
}

export async function fetchAgentSessions(): Promise<AgentSession[]> {
  const data = await hugrQuery(`{
    hub { hub { agent_sessions(order_by: { started_at: desc }, limit: 50) {
      id user_id agent_instance_id started_at ended_at message_count
    } } }
  }`);
  return data?.hub?.hub?.agent_sessions ?? [];
}

export async function fetchLLMProviders(): Promise<LLMProvider[]> {
  const data = await hugrQuery(`{
    hub { hub { llm_providers(order_by: { id: asc }) {
      id provider model base_url max_tokens_per_request enabled
    } } }
  }`);
  return data?.hub?.hub?.llm_providers ?? [];
}

export async function fetchLLMBudgets(): Promise<LLMBudget[]> {
  const data = await hugrQuery(`{
    hub { hub { llm_budgets(order_by: { scope: asc }) {
      id scope scope_id period max_tokens max_requests
    } } }
  }`);
  return data?.hub?.hub?.llm_budgets ?? [];
}

export async function fetchLLMUsage(limit = 100): Promise<LLMUsage[]> {
  const data = await hugrQuery(
    `query($limit: Int!) {
      hub { hub { llm_usage(order_by: { created_at: desc }, limit: $limit) {
        id user_id provider_id input_tokens output_tokens created_at
      } } }
    }`,
    { limit },
  );
  return data?.hub?.hub?.llm_usage ?? [];
}

export async function insertLLMProvider(
  provider: Omit<LLMProvider, 'id'> & { id: string },
): Promise<string> {
  const data = await hugrQuery(
    `mutation($id: String!, $provider: String!, $model: String!, $url: String, $enabled: Boolean) {
      hub { hub { insert_llm_providers(data: {
        id: $id, provider: $provider, model: $model, base_url: $url, enabled: $enabled
      }) { id } } }
    }`,
    {
      id: provider.id,
      provider: provider.provider,
      model: provider.model,
      url: provider.base_url,
      enabled: provider.enabled,
    },
  );
  return data?.hub?.hub?.insert_llm_providers?.id;
}

export async function updateLLMProvider(
  id: string,
  updates: Partial<Omit<LLMProvider, 'id'>>,
): Promise<void> {
  const fields: string[] = [];
  const vars: Record<string, unknown> = { id };
  const params: string[] = ['$id: String!'];

  if (updates.enabled !== undefined) {
    fields.push('enabled: $enabled');
    vars.enabled = updates.enabled;
    params.push('$enabled: Boolean!');
  }
  if (updates.base_url !== undefined) {
    fields.push('base_url: $url');
    vars.url = updates.base_url;
    params.push('$url: String!');
  }
  if (updates.model !== undefined) {
    fields.push('model: $model');
    vars.model = updates.model;
    params.push('$model: String!');
  }

  if (fields.length === 0) return;

  await hugrQuery(
    `mutation(${params.join(', ')}) {
      hub { hub { update_llm_providers(
        filter: { id: { eq: $id } }
        data: { ${fields.join(', ')} }
      ) { affected_rows } } }
    }`,
    vars,
  );
}

export async function insertLLMBudget(
  budget: Omit<LLMBudget, 'id'>,
): Promise<string> {
  const data = await hugrQuery(
    `mutation($scope: String!, $scope_id: String!, $period: String!, $max_tokens: Int!, $max_requests: Int!) {
      hub { hub { insert_llm_budgets(data: {
        scope: $scope, scope_id: $scope_id, period: $period, max_tokens: $max_tokens, max_requests: $max_requests
      }) { id } } }
    }`,
    {
      scope: budget.scope,
      scope_id: budget.scope_id,
      period: budget.period,
      max_tokens: budget.max_tokens,
      max_requests: budget.max_requests,
    },
  );
  return data?.hub?.hub?.insert_llm_budgets?.id;
}

export async function deleteLLMBudget(id: string): Promise<void> {
  await hugrQuery(
    `mutation($id: String!) {
      hub { hub { delete_llm_budgets(filter: { id: { eq: $id } }) { affected_rows } } }
    }`,
    { id },
  );
}

export async function stopAgent(instanceId: string): Promise<void> {
  await hugrQuery(
    `mutation($id: String!) {
      hub { hub { update_agent_instances(
        filter: { id: { eq: $id } }
        data: { status: "stopped" }
      ) { affected_rows } } }
    }`,
    { id: instanceId },
  );
}

export async function clearAgentMemory(userId: string): Promise<void> {
  await hugrQuery(
    `mutation($uid: String!) {
      hub { hub { delete_agent_memory(filter: { user_id: { eq: $uid } }) { affected_rows } } }
    }`,
    { uid: userId },
  );
}
