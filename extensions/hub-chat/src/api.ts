/**
 * Hub Chat API — conversation CRUD and WebSocket connection.
 * Calls hub-chat server extension which proxies to Hub Service.
 */

import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';

// ── Types ──────────────────────────────────────────────

export interface Conversation {
  id: string;
  title: string;
  folder: string | null;
  mode: 'llm' | 'tools' | 'agent';
  agent_instance_id: string | null;
  model: string | null;
  updated_at: string;
  created_at: string;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant' | 'system' | 'tool';
  content: string;
  tool_calls: any | null;
  tool_call_id: string | null;
  tokens_used: number | null;
  model: string | null;
  created_at: string;
}

export interface WsMessage {
  type: 'message' | 'response' | 'error' | 'status';
  content: string;
}

// ── Conversation API ───────────────────────────────────

async function convAPI(action: string, body?: any): Promise<any> {
  const baseUrl = PageConfig.getBaseUrl();
  const settings = ServerConnection.makeSettings();
  const url = baseUrl + 'hub-chat/api/conversations/' + action;
  const resp = await ServerConnection.makeRequest(
    url,
    {
      method: 'POST',
      body: body ? JSON.stringify(body) : '{}',
    },
    settings,
  );
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`Conversation API error: ${resp.status} ${text}`);
  }
  const text = await resp.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    console.warn('Conversation API: non-JSON response:', text);
    return null;
  }
}

export async function createConversation(
  mode: 'llm' | 'tools' | 'agent',
  title?: string,
  folder?: string,
  agentInstanceId?: string,
  model?: string,
): Promise<{ id: string; title: string; mode: string }> {
  const body: any = { mode };
  if (title) body.title = title;
  if (folder) body.folder = folder;
  if (agentInstanceId) body.agent_instance_id = agentInstanceId;
  if (model) body.model = model;
  return convAPI('create', body);
}

export async function listConversations(folder?: string): Promise<Conversation[]> {
  const body: any = {};
  if (folder) body.folder = folder;
  const result = await convAPI('list', body);
  return result ?? [];
}

export async function renameConversation(id: string, title: string): Promise<void> {
  await convAPI('rename', { id, title });
}

export async function deleteConversation(id: string): Promise<void> {
  await convAPI('delete', { id });
}

export async function loadMessages(
  conversationId: string,
  limit = 50,
  before?: string,
): Promise<ChatMessage[]> {
  const body: any = { id: conversationId, limit };
  if (before) body.before = before;
  const result = await convAPI('messages', body);
  return result ?? [];
}

// ── WebSocket ──────────────────────────────────────────

export async function getWsBaseUrl(): Promise<string> {
  const baseUrl = PageConfig.getBaseUrl();
  const settings = ServerConnection.makeSettings();
  const resp = await ServerConnection.makeRequest(
    baseUrl + 'hub-chat/api/config',
    {},
    settings,
  );
  if (!resp.ok) throw new Error('Failed to get chat config');
  const config = await resp.json();
  return config.ws_base;
}

export function connectWebSocket(
  wsBase: string,
  conversationId: string,
  onMessage: (msg: WsMessage) => void,
  onClose: () => void,
  onError: (err: Event) => void,
): WebSocket {
  const url = `${wsBase}/${conversationId}`;
  const ws = new WebSocket(url);

  ws.onmessage = (event) => {
    try {
      const msg: WsMessage = JSON.parse(event.data);
      onMessage(msg);
    } catch {
      onMessage({ type: 'error', content: 'Invalid message from server' });
    }
  };

  ws.onclose = onClose;
  ws.onerror = onError;

  return ws;
}

export function sendMessage(ws: WebSocket, content: string): void {
  ws.send(JSON.stringify({ type: 'message', content }));
}
