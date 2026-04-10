/**
 * Hub Chat WebSocket transport.
 *
 * All REST conversation helpers and agent/model reads moved to
 * `convApiGraphQL.ts` (Hugr GraphQL via hugr_connection_service). This file
 * keeps only the WebSocket path to `/hub-chat/ws/{conversation_id}` and the
 * small helper that resolves the ws base URL from the Python extension.
 */

import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';

// Re-export types that widgets still reference as `Conversation` / `ChatMessage`.
export type { Conversation, ChatMessage, AgentInstance, ModelInfo } from './convApiGraphQL.js';

export interface UsageInfo {
  tokens_in: number;
  tokens_out: number;
  model: string;
}

export interface WsMessage {
  type: 'message' | 'token' | 'thinking' | 'tool_call' | 'tool_result' |
        'response' | 'error' | 'status' | 'title_update' | 'summary' | 'cancel';
  content: string;
  tool_calls?: any[];
  tool_call_id?: string;
  agent_name?: string;
  usage?: UsageInfo;
  summary_of?: string[];
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

export function sendMessage(ws: WebSocket, content: string, messages?: any[]): void {
  const payload: any = { type: 'message', content };
  if (messages) payload.messages = messages;
  ws.send(JSON.stringify(payload));
}

export function sendCancel(ws: WebSocket): void {
  ws.send(JSON.stringify({ type: 'cancel' }));
}

export function sendSummarize(ws: WebSocket, conversationId: string, upToMessageId: string): void {
  ws.send(JSON.stringify({ type: 'summarize', conversation_id: conversationId, up_to_message_id: upToMessageId }));
}
