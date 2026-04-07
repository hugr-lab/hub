/**
 * Hugr Admin API — Data Sources, Catalogs, Models, Budgets, Usage, Agents.
 * Uses HugrClient with hugr_connection_service proxy.
 */

import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';
import { HugrClient } from './hugrClient.js';

// ── Types ────────────────────────────────────────────────

export interface DataSource {
  name: string;
  type: string;
  prefix: string;
  path: string;
  description: string;
  disabled: boolean;
  read_only: boolean;
  as_module: boolean;
  self_defined: boolean;
}

export interface CatalogSource {
  name: string;
  type: string;
  path: string;
  description: string;
}

export interface CatalogLink {
  catalog_name: string;
  data_source_name: string;
}

export interface ModelSource {
  name: string;
  type: string;
  provider: string;
  model: string;
}

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

// ── Client ───────────────────────────────────────────────

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

async function hugrQuery(query: string, variables?: Record<string, unknown>): Promise<any> {
  const client = await getClient();
  const result = await client.query(query, variables);
  if (result.errors.length > 0) {
    throw new Error(result.errors.map(e => e.message).join(', '));
  }
  return result.data;
}

// ── Data Sources (core.*) ────────────────────────────────

export async function fetchDataSources(): Promise<DataSource[]> {
  const data = await hugrQuery(`{
    core { data_sources {
      name type prefix path description disabled read_only as_module self_defined
    } }
  }`);
  return data?.core?.data_sources ?? [];
}

export async function fetchDataSourceStatus(name: string): Promise<string> {
  const data = await hugrQuery(
    `query($name: String!) {
      function { core { data_source_status(name: $name) } }
    }`,
    { name },
  );
  return data?.function?.core?.data_source_status ?? 'unknown';
}

export async function insertDataSource(ds: Partial<DataSource> & { name: string; type: string; path: string; prefix: string }, catalogs?: Array<{ name: string; type: string; path: string }>): Promise<void> {
  const dsData: Record<string, unknown> = { ...ds };
  if (catalogs && catalogs.length > 0) {
    dsData.catalogs = catalogs;
  }
  await hugrQuery(
    `mutation($data: core_data_sources_mut_input_data!) {
      core { insert_data_sources(data: $data) { name } }
    }`,
    { data: dsData },
  );
}

export async function updateDataSource(name: string, updates: Partial<DataSource>): Promise<void> {
  await hugrQuery(
    `mutation($filter: core_data_sources_filter!, $data: core_data_sources_mut_data!) {
      core { update_data_sources(filter: $filter, data: $data) { affected_rows } }
    }`,
    { filter: { name: { eq: name } }, data: updates },
  );
}

export async function deleteDataSource(name: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: core_data_sources_filter!) {
      core { delete_data_sources(filter: $filter) { affected_rows } }
    }`,
    { filter: { name: { eq: name } } },
  );
}

export interface DSOperationResult {
  success: boolean;
  message: string;
}

export async function loadDataSource(name: string): Promise<DSOperationResult> {
  const data = await hugrQuery(
    `mutation($name: String!) {
      function { core { load_data_source(name: $name) { affected_rows success message } } }
    }`,
    { name },
  );
  const r = data?.function?.core?.load_data_source;
  return { success: r?.success ?? false, message: r?.message ?? '' };
}

export async function unloadDataSource(name: string, hard = false): Promise<DSOperationResult> {
  const data = await hugrQuery(
    `mutation($name: String!, $hard: Boolean) {
      function { core { unload_data_source(name: $name, hard: $hard) { affected_rows success message } } }
    }`,
    { name, hard },
  );
  const r = data?.function?.core?.unload_data_source;
  return { success: r?.success ?? false, message: r?.message ?? '' };
}

// ── Catalogs (core.*) ────────────────────────────────────

export async function fetchCatalogSources(): Promise<CatalogSource[]> {
  const data = await hugrQuery(`{
    core { catalog_sources { name type path description } }
  }`);
  return data?.core?.catalog_sources ?? [];
}

export async function fetchCatalogs(): Promise<CatalogLink[]> {
  const data = await hugrQuery(`{
    core { catalogs { catalog_name data_source_name } }
  }`);
  return data?.core?.catalogs ?? [];
}

export async function insertCatalogSource(cs: { name: string; type: string; path: string; description?: string }): Promise<void> {
  await hugrQuery(
    `mutation($data: core_catalog_sources_mut_input_data!) {
      core { insert_catalog_sources(data: $data) { name } }
    }`,
    { data: cs },
  );
}

export async function updateCatalogSource(name: string, updates: Partial<CatalogSource>): Promise<void> {
  await hugrQuery(
    `mutation($filter: core_catalog_sources_filter!, $data: core_catalog_sources_mut_data!) {
      core { update_catalog_sources(filter: $filter, data: $data) { affected_rows } }
    }`,
    { filter: { name: { eq: name } }, data: updates },
  );
}

export async function deleteCatalogSource(name: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: core_catalog_sources_filter!) {
      core { delete_catalog_sources(filter: $filter) { affected_rows } }
    }`,
    { filter: { name: { eq: name } } },
  );
}

export async function linkCatalog(catalogName: string, dataSourceName: string): Promise<void> {
  await hugrQuery(
    `mutation($data: core_catalogs_mut_input_data!) {
      core { insert_catalogs(data: $data) { catalog_name } }
    }`,
    { data: { catalog_name: catalogName, data_source_name: dataSourceName } },
  );
}

export async function unlinkCatalog(catalogName: string, dataSourceName: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: core_catalogs_filter!) {
      core { delete_catalogs(filter: $filter) { affected_rows } }
    }`,
    { filter: { catalog_name: { eq: catalogName }, data_source_name: { eq: dataSourceName } } },
  );
}

// ── Models (function.core.models.*) ──────────────────────

export async function fetchModelSources(): Promise<ModelSource[]> {
  const data = await hugrQuery(`{
    function { core { models {
      model_sources { name type provider model }
    } } }
  }`);
  return data?.function?.core?.models?.model_sources ?? [];
}

// ── Agents (hub.db.*) ────────────────────────────────────

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

export async function stopAgentDB(instanceId: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: hub_db_agent_instances_filter!, $data: hub_db_agent_instances_mut_data!) {
      hub { db { update_agent_instances(filter: $filter, data: $data) { affected_rows } } }
    }`,
    { filter: { id: { eq: instanceId } }, data: { status: 'stopped' } },
  );
}

// ── Hub Service API (via proxy) ─────────────────────────

async function hubServiceAPI(path: string, method = 'GET', body?: any): Promise<any> {
  const baseUrl = PageConfig.getBaseUrl();
  const settings = ServerConnection.makeSettings();
  const url = baseUrl + 'hub-admin/api/hub/' + path.replace(/^\//, '');
  const init: RequestInit = { method };
  if (body) {
    init.body = JSON.stringify(body);
    init.headers = { 'Content-Type': 'application/json' };
  }
  const resp = await ServerConnection.makeRequest(url, init, settings);
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`Hub Service error: ${resp.status} ${text}`);
  }
  return resp.json();
}

export interface AgentType {
  id: string;
  display_name: string;
  description: string;
  image: string;
}

export async function fetchAgentTypes(): Promise<AgentType[]> {
  const data = await hugrQuery(`{
    hub { db { agent_types {
      id display_name description image
    } } }
  }`);
  return data?.hub?.db?.agent_types ?? [];
}

export async function startAgent(userId: string, agentTypeId: string): Promise<{ status: string; container_id: string }> {
  return hubServiceAPI('api/agent/start', 'POST', { user_id: userId, agent_type_id: agentTypeId });
}

export async function stopAgent(userId: string): Promise<{ status: string }> {
  return hubServiceAPI('api/agent/stop', 'POST', { user_id: userId });
}

export async function clearAgentMemory(userId: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: hub_db_agent_memory_filter!) {
      hub { db { delete_agent_memory(filter: $filter) { affected_rows } } }
    }`,
    { filter: { user_id: { eq: userId } } },
  );
}

// ── Budgets (hub.db.*) ───────────────────────────────────

export async function fetchLLMBudgets(): Promise<LLMBudget[]> {
  const data = await hugrQuery(`{
    hub { db { llm_budgets(order_by: [{field: "scope", direction: ASC}]) {
      id scope provider_id period max_tokens_in max_tokens_out max_requests
    } } }
  }`);
  return data?.hub?.db?.llm_budgets ?? [];
}

export async function insertLLMBudget(budget: Omit<LLMBudget, 'id'>): Promise<void> {
  await hugrQuery(
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
}

export async function deleteLLMBudget(id: string): Promise<void> {
  await hugrQuery(
    `mutation($filter: hub_db_llm_budgets_filter!) {
      hub { db { delete_llm_budgets(filter: $filter) { affected_rows } } }
    }`,
    { filter: { id: { eq: id } } },
  );
}

// ── Usage (hub.db.*) ─────────────────────────────────────

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
