/**
 * GraphQL-based conversation + runtime operations.
 *
 * Goes straight to Hugr via `hugr_connection_service` (the JupyterLab server
 * extension adds the OIDC bearer token). All write paths are airport-go
 * mutating functions registered by hub-service, all read paths are airport-go
 * table functions — so identity is injected server-side via
 * ArgFromContext(user_id, user_name, role, auth_type) and the browser never
 * needs to know its own user_id.
 *
 * Backend contracts:
 *   - pkg/hubapp/handlers_conversation.go — create/rename/delete/move/branch/summarize
 *   - pkg/hubapp/handlers_reads.go       — my_conversations, my_conversation_messages, my_agent_instances
 *   - pkg/hubapp/catalog.go              — registration entry point
 */

import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';

// ── Types ─────────────────────────────────────────────────

export interface Conversation {
  id: string;
  user_id: string;
  title: string;
  folder: string | null;
  mode: 'llm' | 'tools' | 'agent';
  agent_type_id: string | null;
  agent_id: string | null;
  model: string | null;
  parent_id: string | null;
  branch_point_message_id: string | null;
  branch_label: string | null;
  created_at: string;
  updated_at: string;
}

export interface ChatMessage {
  id: string;
  conversation_id: string;
  role: 'user' | 'assistant' | 'system' | 'tool';
  content: string;
  tool_calls?: any;
  tool_call_id?: string | null;
  tokens_used?: number | null;
  model?: string | null;
  is_summary: boolean;
  summarized_by?: string | null;
  created_at: string;
}

export interface SummaryItem {
  position: number;
  original_message: ChatMessage;
}

export interface ConversationHandle {
  id: string;
  title: string;
  mode: string;
  parent_id: string | null;
  branch_point_message_id: string | null;
}

export interface SummarizeResult {
  id: string;
  summary_text: string;
  message_count: number;
}

export interface ModelInfo {
  name: string;
  type: string;
  provider: string;
  model: string;
}

export interface AgentInstance {
  id: string;
  agent_type_id: string;
  display_name: string;
  hugr_role: string;
  status: string;
  connected: boolean;
  access_role: string;
}

// ── Hugr GraphQL client ──────────────────────────────────

let _hugrUrl: string | null = null;

async function getHugrUrl(): Promise<string> {
  if (_hugrUrl) return _hugrUrl;
  const baseUrl = PageConfig.getBaseUrl();
  const settings = ServerConnection.makeSettings();
  const resp = await ServerConnection.makeRequest(baseUrl + 'hugr/connections', {}, settings);
  if (!resp.ok) throw new Error(`Failed to get hugr connections: ${resp.status}`);
  const conns: Array<{ name: string; status?: string }> = await resp.json();
  const def = conns.find(c => c.status === 'default') || conns[0];
  if (!def) throw new Error('No Hugr connection configured');
  _hugrUrl = baseUrl + 'hugr/proxy/' + encodeURIComponent(def.name);
  return _hugrUrl;
}

async function gqlQuery(query: string, variables?: Record<string, unknown>): Promise<any> {
  const url = await getHugrUrl();
  const settings = ServerConnection.makeSettings();
  const resp = await ServerConnection.makeRequest(
    url,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, variables: variables ?? {} }),
    },
    settings,
  );
  if (!resp.ok) throw new Error(`GraphQL HTTP ${resp.status}: ${await resp.text()}`);
  const result = await resp.json();
  if (result.errors && result.errors.length > 0) {
    throw new Error(result.errors.map((e: any) => e.message).join(', '));
  }
  return result.data;
}

// ── Conversations: reads ─────────────────────────────────

export async function listConversations(folder?: string, limit = 100): Promise<Conversation[]> {
  const data = await gqlQuery(
    `query($folder: String, $limit: BigInt) {
      hub { my_conversations(folder: $folder, limit: $limit) {
        id user_id title folder mode agent_type_id agent_id model
        parent_id branch_point_message_id branch_label
        created_at updated_at
      } }
    }`,
    { folder: folder ?? '', limit },
  );
  return data?.hub?.my_conversations ?? [];
}

/**
 * Load messages for a conversation. Ownership is verified server-side inside
 * my_conversation_messages; no client-side check needed.
 */
export async function loadMessages(conversationId: string, limit = 200, before?: string): Promise<ChatMessage[]> {
  const data = await gqlQuery(
    `query($cid: String!, $limit: BigInt, $before: String) {
      hub { my_conversation_messages(conversation_id: $cid, limit: $limit, before: $before) {
        id conversation_id role content tool_calls tool_call_id tokens_used model
        is_summary summarized_by created_at
      } }
    }`,
    { cid: conversationId, limit, before: before ?? '' },
  );
  return data?.hub?.my_conversation_messages ?? [];
}

/**
 * Load messages plus, for any summary message, the original messages it covers
 * with their position. Uses a second @field_references query directly against
 * hub.db.agent_messages — the ownership check on my_conversation_messages
 * already gated the conversation, so this follow-up is safe.
 */
export async function loadMessagesWithSummaries(conversationId: string): Promise<Array<ChatMessage & { summary_items?: SummaryItem[] }>> {
  // First verify access via my_conversation_messages (one row is enough).
  await loadMessages(conversationId, 1);
  const data = await gqlQuery(
    `query($cid: String!) {
      hub { db { agent_messages(
        filter: { conversation_id: { eq: $cid } }
      ) {
        id conversation_id role content tool_calls tool_call_id is_summary summarized_by created_at
        summary_items {
          position
          original_message {
            id role content tool_calls tool_call_id created_at
          }
        }
      } } }
    }`,
    { cid: conversationId },
  );
  return data?.hub?.db?.agent_messages ?? [];
}

/**
 * Recursive conversation_context view (Spec A+) — walks parent links to collect
 * messages across branches. The view takes conversation_id as an @args input.
 */
export async function loadConversationContext(conversationId: string): Promise<ChatMessage[]> {
  // Access check (one message). If the conversation is not owned, error bubbles.
  await loadMessages(conversationId, 1);
  const data = await gqlQuery(
    `query($cid: String!) {
      hub { db { conversation_context(args: { conversation_id: $cid }) {
        id conversation_id role content tool_calls tool_call_id tokens_used model
        is_summary summarized_by created_at
      } } }
    }`,
    { cid: conversationId },
  );
  return data?.hub?.db?.conversation_context ?? [];
}

// ── Conversations: writes ────────────────────────────────

export async function createConversation(input: {
  mode: 'llm' | 'tools' | 'agent';
  title?: string;
  folder?: string;
  model?: string;
  agent_id?: string;
  agent_type_id?: string;
}): Promise<ConversationHandle> {
  const data = await gqlQuery(
    `mutation($input: create_conversation_input!) {
      function { hub { create_conversation(input: $input) {
        id title mode parent_id branch_point_message_id
      } } }
    }`,
    { input },
  );
  return data?.function?.hub?.create_conversation;
}

export async function renameConversation(id: string, title: string): Promise<void> {
  await gqlQuery(
    `mutation($id: String!, $title: String!) {
      function { hub { rename_conversation(id: $id, title: $title) } }
    }`,
    { id, title },
  );
}

export async function deleteConversation(id: string): Promise<void> {
  await gqlQuery(
    `mutation($id: String!) {
      function { hub { delete_conversation(id: $id) } }
    }`,
    { id },
  );
}

export async function moveConversation(id: string, folder: string | null): Promise<void> {
  await gqlQuery(
    `mutation($id: String!, $folder: String!) {
      function { hub { move_conversation(id: $id, folder: $folder) } }
    }`,
    { id, folder: folder ?? '' },
  );
}

export async function branchConversation(input: {
  parent_id: string;
  branch_point_message_id: string;
  branch_label?: string;
  title?: string;
}): Promise<ConversationHandle> {
  const data = await gqlQuery(
    `mutation($input: branch_conversation_input!) {
      function { hub { branch_conversation(input: $input) {
        id title mode parent_id branch_point_message_id
      } } }
    }`,
    { input },
  );
  return data?.function?.hub?.branch_conversation;
}

export async function summarizeMessages(conversationId: string, upToMessageId: string): Promise<SummarizeResult> {
  const data = await gqlQuery(
    `mutation($cid: String!, $mid: String!) {
      function { hub { summarize_conversation(
        conversation_id: $cid
        up_to_message_id: $mid
      ) { id summary_text message_count } }
    } }`,
    { cid: conversationId, mid: upToMessageId },
  );
  return data?.function?.hub?.summarize_conversation;
}

// ── Models ────────────────────────────────────────────────

let _cachedModels: ModelInfo[] | null = null;
let _hasLLM: boolean | null = null;

export async function listModels(): Promise<ModelInfo[]> {
  if (_cachedModels) return _cachedModels;
  try {
    const data = await gqlQuery(`{
      function { core { models { model_sources { name type provider model } } } }
    }`);
    const all: ModelInfo[] = data?.function?.core?.models?.model_sources ?? [];
    _cachedModels = all.filter(m => m.type === 'llm');
    _hasLLM = _cachedModels.length > 0;
    return _cachedModels;
  } catch {
    _cachedModels = [];
    _hasLLM = false;
    return [];
  }
}

/** Returns true if LLM models are available. Call listModels() first. */
export function hasLLM(): boolean {
  return _hasLLM ?? false;
}

// ── Agent instances (for sidebar "New Chat → Agent") ──────

export async function listAgentInstances(): Promise<AgentInstance[]> {
  try {
    const data = await gqlQuery(`{
      hub { my_agent_instances {
        id agent_type_id display_name hugr_role status connected access_role
      } }
    }`);
    return data?.hub?.my_agent_instances ?? [];
  } catch {
    return [];
  }
}
