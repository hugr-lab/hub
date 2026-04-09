/**
 * ChatDocument — main area widget for a single conversation.
 * Client owns the full message history (including tool_calls/tool_results).
 * Hub Service is a stateless proxy to LLM.
 */
import { Widget } from '@lumino/widgets';
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';
import { loadMessages, getWsBaseUrl, connectWebSocket, WsMessage } from '../api.js';
import { MessageRenderer } from './MessageRenderer.js';

interface HistoryMessage {
  role: string;
  content: string;
  tool_calls?: any;
  tool_call_id?: string;
}

export class ChatDocumentWidget extends Widget {
  private conversationId: string;
  private renderer: MessageRenderer;
  private messagesEl: HTMLDivElement;
  private inputEl: HTMLTextAreaElement;
  private stopBtn: HTMLButtonElement;
  private statusEl: HTMLDivElement;
  private processing = false;
  private ws: WebSocket | null = null;
  private loading = false;
  private oldestTimestamp: string | null = null;
  private hasMore = true;

  // Full conversation history — sent to Hub Service with each message
  private chatHistory: HistoryMessage[] = [];

  constructor(conversationId: string, title: string, rendermime: IRenderMimeRegistry) {
    super();
    this.conversationId = conversationId;
    this.renderer = new MessageRenderer(rendermime);
    this.addClass('hub-chat-document');
    this.title.label = title;
    this.title.closable = true;

    // System prompt
    this.chatHistory.push({
      role: 'system',
      content: 'You are a helpful data assistant for Analytics Hub. You have access to Hugr Data Mesh tools for data discovery, schema exploration, and query execution. Use tools to find and analyze data.',
    });

    this.messagesEl = document.createElement('div');
    this.messagesEl.className = 'hub-chat-messages';
    this.messagesEl.addEventListener('scroll', () => this.onScroll());
    this.node.appendChild(this.messagesEl);

    const inputArea = document.createElement('div');
    inputArea.className = 'hub-chat-input-area';

    this.inputEl = document.createElement('textarea');
    this.inputEl.className = 'hub-chat-input';
    this.inputEl.placeholder = 'Type a message... (Enter to send, Shift+Enter for newline)';
    this.inputEl.rows = 1;
    this.inputEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.handleSend();
      }
    });
    this.inputEl.addEventListener('input', () => {
      this.inputEl.style.height = 'auto';
      this.inputEl.style.height = Math.min(this.inputEl.scrollHeight, 120) + 'px';
    });
    inputArea.appendChild(this.inputEl);

    const sendBtn = document.createElement('button');
    sendBtn.className = 'hub-chat-send-btn';
    sendBtn.textContent = 'Send';
    sendBtn.addEventListener('click', () => this.handleSend());

    this.stopBtn = document.createElement('button');
    this.stopBtn.className = 'hub-chat-stop-btn';
    this.stopBtn.textContent = 'Stop';
    this.stopBtn.style.display = 'none';
    this.stopBtn.addEventListener('click', () => this.handleStop());
    inputArea.appendChild(sendBtn);
    inputArea.appendChild(this.stopBtn);
    this.node.appendChild(inputArea);

    this.statusEl = document.createElement('div');
    this.statusEl.className = 'hub-chat-status-bar';
    this.statusEl.textContent = 'connecting...';
    this.node.appendChild(this.statusEl);

    this.initialize();
  }

  dispose(): void {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    super.dispose();
  }

  private async initialize(): Promise<void> {
    await this.loadInitialMessages();
    try {
      const wsBase = await getWsBaseUrl();
      this.ws = connectWebSocket(
        wsBase,
        this.conversationId,
        (msg) => this.onWsMessage(msg),
        () => this.updateStatus('disconnected'),
        () => this.updateStatus('error'),
      );
      this.ws.onopen = () => this.updateStatus('connected');
    } catch {
      this.updateStatus('connection failed');
    }
  }

  private async loadInitialMessages(): Promise<void> {
    try {
      const messages = await loadMessages(this.conversationId, 50);
      messages.reverse();
      for (const msg of messages) {
        // Rebuild chatHistory from DB
        const histMsg: HistoryMessage = { role: msg.role, content: msg.content };
        if (msg.tool_calls) histMsg.tool_calls = msg.tool_calls;
        if (msg.tool_call_id) histMsg.tool_call_id = msg.tool_call_id;
        this.chatHistory.push(histMsg);

        // Only show user/assistant messages in UI (not tool messages)
        if (msg.role === 'user' || msg.role === 'assistant') {
          await this.appendMessage(msg.role, msg.content, msg.created_at);
        }
      }
      if (messages.length > 0) {
        this.oldestTimestamp = messages[0].created_at;
      }
      this.hasMore = messages.length === 50;
      this.scrollToBottom();
    } catch (err: any) {
      this.messagesEl.innerHTML = `<div class="hub-chat-error">Failed to load messages: ${err.message}</div>`;
    }
  }

  private async loadOlderMessages(): Promise<void> {
    if (this.loading || !this.hasMore || !this.oldestTimestamp) return;
    this.loading = true;
    try {
      const messages = await loadMessages(this.conversationId, 50, this.oldestTimestamp);
      messages.reverse();
      const scrollHeight = this.messagesEl.scrollHeight;
      for (const msg of messages) {
        if (msg.role === 'user' || msg.role === 'assistant') {
          const el = await this.renderer.render(msg.role, msg.content, msg.created_at);
          this.messagesEl.insertBefore(el, this.messagesEl.firstChild);
        }
      }
      this.messagesEl.scrollTop = this.messagesEl.scrollHeight - scrollHeight;
      if (messages.length > 0) {
        this.oldestTimestamp = messages[0].created_at;
      }
      this.hasMore = messages.length === 50;
    } finally {
      this.loading = false;
    }
  }

  private onScroll(): void {
    if (this.messagesEl.scrollTop < 100 && this.hasMore) {
      this.loadOlderMessages();
    }
  }

  private handleStop(): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'cancel' }));
    }
    this.processing = false;
    this.stopBtn.style.display = 'none';
    this.removeStatus();
  }

  private async handleSend(): Promise<void> {
    const text = this.inputEl.value.trim();
    if (!text || !this.ws || this.ws.readyState !== WebSocket.OPEN) return;

    this.inputEl.value = '';
    this.inputEl.style.height = 'auto';
    this.processing = true;
    this.stopBtn.style.display = '';

    // Add to history and show
    this.chatHistory.push({ role: 'user', content: text });
    await this.appendMessage('user', text);
    this.scrollToBottom();

    // Send full history to Hub Service
    this.ws.send(JSON.stringify({
      type: 'message',
      content: text,
      messages: this.chatHistory,
    }));
  }

  private statusElement: HTMLElement | null = null;

  private async onWsMessage(msg: WsMessage): Promise<void> {
    switch (msg.type) {
      case 'response':
        this.removeStatus();
        this.processing = false;
        this.stopBtn.style.display = 'none';
        // Add to history
        this.chatHistory.push({ role: 'assistant', content: msg.content });
        await this.appendMessage('assistant', msg.content);
        this.scrollToBottom();
        break;

      case 'tool_call':
        // LLM wants to call tools — add assistant message with tool_calls to history
        this.chatHistory.push({
          role: 'assistant',
          content: msg.content || '',
          tool_calls: msg.tool_calls,
        });
        // Show tool calls in UI as status
        if (Array.isArray(msg.tool_calls)) {
          const names = msg.tool_calls.map((tc: any) => tc.name).join(', ');
          this.showStatus(`calling: ${names}`);
        }
        break;

      case 'tool_result':
        // Tool result — add to history
        this.chatHistory.push({
          role: 'tool',
          content: msg.content,
          tool_call_id: msg.tool_call_id,
        });
        break;

      case 'status':
        if (msg.content === 'cancelled') {
          this.removeStatus();
          this.processing = false;
          this.stopBtn.style.display = 'none';
          await this.appendMessage('system', 'Cancelled by user');
          this.scrollToBottom();
        } else {
          this.showStatus(msg.content);
        }
        break;

      case 'error':
        this.removeStatus();
        this.processing = false;
        this.stopBtn.style.display = 'none';
        await this.appendMessage('system', `Error: ${msg.content}`);
        this.scrollToBottom();
        break;
    }
  }

  private async appendMessage(role: string, content: string, timestamp?: string): Promise<void> {
    const el = await this.renderer.render(role, content, timestamp);
    this.messagesEl.appendChild(el);
  }

  private showStatus(content: string): void {
    this.removeStatus();
    this.statusElement = this.renderer.renderStatus(content);
    this.messagesEl.appendChild(this.statusElement);
    this.scrollToBottom();
  }

  private removeStatus(): void {
    if (this.statusElement) {
      this.statusElement.remove();
      this.statusElement = null;
    }
  }

  private scrollToBottom(): void {
    this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
  }

  private updateStatus(status: string): void {
    this.statusEl.textContent = status;
    this.statusEl.className = `hub-chat-status-bar hub-chat-status-bar--${status}`;
  }
}
