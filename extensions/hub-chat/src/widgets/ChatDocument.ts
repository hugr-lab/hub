/**
 * ChatDocument — main area widget for a single conversation.
 * Displays message history, input area, status bar.
 * Connects to Hub Service via WebSocket for real-time messaging.
 */
import { Widget } from '@lumino/widgets';
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';
import {
  loadMessages, getWsBaseUrl, connectWebSocket, sendMessage as wsSend,
  ChatMessage, WsMessage,
} from '../api.js';
import { MessageRenderer } from './MessageRenderer.js';

export class ChatDocumentWidget extends Widget {
  private conversationId: string;
  private renderer: MessageRenderer;
  private messagesEl: HTMLDivElement;
  private inputEl: HTMLTextAreaElement;
  private statusEl: HTMLDivElement;
  private ws: WebSocket | null = null;
  private loading = false;
  private oldestTimestamp: string | null = null;
  private hasMore = true;

  constructor(conversationId: string, title: string, rendermime: IRenderMimeRegistry) {
    super();
    this.conversationId = conversationId;
    this.renderer = new MessageRenderer(rendermime);
    this.addClass('hub-chat-document');
    this.title.label = title;
    this.title.closable = true;

    // Messages area
    this.messagesEl = document.createElement('div');
    this.messagesEl.className = 'hub-chat-messages';
    this.messagesEl.addEventListener('scroll', () => this.onScroll());
    this.node.appendChild(this.messagesEl);

    // Input area
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
    inputArea.appendChild(sendBtn);

    this.node.appendChild(inputArea);

    // Status bar
    this.statusEl = document.createElement('div');
    this.statusEl.className = 'hub-chat-status-bar';
    this.statusEl.textContent = 'connecting...';
    this.node.appendChild(this.statusEl);

    // Load messages and connect
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
    // Load recent messages
    await this.loadInitialMessages();

    // Connect WebSocket
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
    } catch (err: any) {
      this.updateStatus('connection failed');
    }
  }

  private async loadInitialMessages(): Promise<void> {
    try {
      const messages = await loadMessages(this.conversationId, 50);
      // Messages come newest-first, reverse for display
      messages.reverse();
      for (const msg of messages) {
        await this.appendMessage(msg.role, msg.content, msg.created_at);
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
        const el = await this.renderer.render(msg.role, msg.content, msg.created_at);
        this.messagesEl.insertBefore(el, this.messagesEl.firstChild);
      }
      // Maintain scroll position
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

  private async handleSend(): Promise<void> {
    const text = this.inputEl.value.trim();
    if (!text || !this.ws || this.ws.readyState !== WebSocket.OPEN) return;

    this.inputEl.value = '';
    this.inputEl.style.height = 'auto';

    // Show user message immediately
    await this.appendMessage('user', text);
    this.scrollToBottom();

    // Send via WebSocket
    wsSend(this.ws, text);
  }

  private statusElement: HTMLElement | null = null;

  private async onWsMessage(msg: WsMessage): Promise<void> {
    switch (msg.type) {
      case 'response':
        this.removeStatus();
        await this.appendMessage('assistant', msg.content);
        this.scrollToBottom();
        break;
      case 'status':
        this.showStatus(msg.content);
        break;
      case 'error':
        this.removeStatus();
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
