/**
 * ChatDocument — main area widget for a single conversation.
 * Supports streaming tokens, tool call visibility, and summarization.
 */
import { Widget } from '@lumino/widgets';
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';
import { getWsBaseUrl, connectWebSocket, sendCancel, WsMessage } from '../api.js';
import {
  loadMessages, loadMessagesWithSummaries, summarizeMessages, branchConversation,
} from '../convApiGraphQL.js';
import { MessageRenderer, normalizeFrame } from './MessageRenderer.js';

interface HistoryMessage {
  role: string;
  content: string;
  tool_calls?: any;
  tool_call_id?: string;
}

export class ChatDocumentWidget extends Widget {
  readonly conversationId: string;
  private renderer: MessageRenderer;
  onTitleChange: ((newTitle: string) => void) | null = null;
  onBranch: ((branchId: string, title: string) => void) | null = null;
  private messagesEl: HTMLDivElement;
  private inputEl: HTMLTextAreaElement;
  private stopBtn: HTMLButtonElement;
  private statusEl: HTMLDivElement;
  private processing = false;
  private ws: WebSocket | null = null;
  private wsBase: string | null = null;
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private _scrollHandler: (() => void) | null = null;
  private loading = false;
  private oldestTimestamp: string | null = null;
  private hasMore = true;
  private chatHistory: HistoryMessage[] = [];

  // Streaming state
  private streamingEl: HTMLElement | null = null;
  private thinkingEl: HTMLElement | null = null;
  private toolCallsEl: HTMLElement | null = null;
  private statusElement: HTMLElement | null = null;
  /** Wall-clock time (epoch ms) when the user message was sent — used to
   * compute response duration in the assistant footer. */
  private sendStartedAt: number | null = null;

  // Message ID tracking for branching/summarization
  private messageIds: string[] = [];

  constructor(conversationId: string, title: string, rendermime: IRenderMimeRegistry) {
    super();
    this.conversationId = conversationId;
    this.renderer = new MessageRenderer(rendermime);
    this.addClass('hub-chat-document');
    this.title.label = title;
    this.title.closable = true;

    this.chatHistory.push({
      role: 'system',
      content: 'You are a helpful data assistant for Analytics Hub. You have access to Hugr Data Mesh tools for data discovery, schema exploration, and query execution. Use tools to find and analyze data.',
    });

    this.messagesEl = document.createElement('div');
    this.messagesEl.className = 'hub-chat-messages';
    this._scrollHandler = () => this.onScroll();
    this.messagesEl.addEventListener('scroll', this._scrollHandler);
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
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this._scrollHandler && this.messagesEl) {
      this.messagesEl.removeEventListener('scroll', this._scrollHandler);
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    // Clear any in-flight streaming/thinking ticker intervals so they don't
    // keep running after the widget is gone, holding closure references on
    // detached DOM nodes.
    this.finalizeStream();
    super.dispose();
  }

  private async initialize(): Promise<void> {
    await this.loadInitialMessages();
    try {
      this.wsBase = await getWsBaseUrl();
      this.connectWs();
    } catch {
      this.updateStatus('connection failed');
    }
  }

  private connectWs(): void {
    if (!this.wsBase || this.isDisposed) return;

    // Close existing connection if any (prevent parallel connections)
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.onerror = null;
      this.ws.close();
      this.ws = null;
    }

    this.ws = connectWebSocket(
      this.wsBase,
      this.conversationId,
      (msg) => this.onWsMessage(msg),
      () => this.onWsClose(),
      () => {}, // onerror → onclose fires next, don't double-trigger
    );
    this.ws.onopen = () => {
      this.reconnectAttempt = 0;
      this.updateStatus('connected');
    };
  }

  private onWsClose(): void {
    if (this.isDisposed) return;
    this.ws = null;
    this.processing = false;
    this.stopBtn.style.display = 'none';
    this.removeStatusMsg();
    this.finalizeStream();

    // Cancel any pending reconnect to prevent overlapping timers
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempt), 30000);
    this.reconnectAttempt++;
    this.updateStatus(`reconnecting in ${Math.round(delay / 1000)}s...`);

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.isDisposed) {
        this.updateStatus('reconnecting...');
        this.connectWs();
      }
    }, delay);
  }

  private async loadInitialMessages(): Promise<void> {
    try {
      // loadMessagesWithSummaries returns every message in the conversation
      // with the summary_items reverse relation pre-joined, so we can render
      // summary blocks without a second fetch. The rare cost (full fetch on
      // open) is fine for typical conversations (<500 msgs); pagination for
      // longer history uses loadMessages in loadOlderMessages below.
      const messages = await loadMessagesWithSummaries(this.conversationId);
      // Apache Arrow Timestamp columns deserialize as JS Date objects, not
      // ISO strings, so localeCompare fails. Coerce to numeric epoch ms for
      // both sides — works for Date, ISO string, and numeric epoch input.
      const epoch = (v: unknown): number => {
        if (v instanceof Date) return v.getTime();
        if (typeof v === 'number') return v;
        if (typeof v === 'string') return new Date(v).getTime();
        return 0;
      };
      messages.sort((a, b) => epoch(a.created_at) - epoch(b.created_at));
      for (const msg of messages) {
        const histMsg: HistoryMessage = { role: msg.role, content: msg.content };
        if (msg.tool_calls) histMsg.tool_calls = msg.tool_calls;
        if (msg.tool_call_id) histMsg.tool_call_id = msg.tool_call_id;
        this.chatHistory.push(histMsg);
        this.messageIds.push(msg.id);

        // Show summary blocks — originals are embedded via summary_items.
        if (msg.is_summary && msg.summary_items && msg.summary_items.length > 0) {
          const originals = [...msg.summary_items]
            .sort((a, b) => a.position - b.position)
            .map(item => item.original_message);
          const block = this.renderer.renderSummaryBlock(
            originals.length,
            msg.content,
            () => this.renderOriginalMessages(originals),
          );
          this.messagesEl.appendChild(block);
        } else if (!msg.summarized_by && (msg.role === 'user' || msg.role === 'assistant')) {
          const el = await this.appendMessage(msg.role, msg.content, msg.created_at, msg.id);
          this.addMessageContextMenu(el, msg.id);
        }
      }
      if (messages.length > 0) {
        this.oldestTimestamp = messages[0].created_at;
      }
      this.hasMore = false; // full fetch — nothing older to load
      this.scrollToBottom();
    } catch (err: any) {
      const errEl = document.createElement('div');
      errEl.className = 'hub-chat-error';
      errEl.textContent = `Failed to load messages: ${err.message}`;
      this.messagesEl.innerHTML = '';
      this.messagesEl.appendChild(errEl);
    }
  }

  private async renderOriginalMessages(
    originals: Array<{ id: string; role: string; content: string; created_at: string }>,
  ): Promise<void> {
    for (const msg of originals) {
      if (msg.role === 'user' || msg.role === 'assistant') {
        await this.appendMessage(msg.role, msg.content, msg.created_at);
      }
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
      sendCancel(this.ws);
    }
    this.processing = false;
    this.stopBtn.style.display = 'none';
    this.removeStatusMsg();
    this.finalizeStream();
  }

  private async handleSend(): Promise<void> {
    const text = this.inputEl.value.trim();
    if (!text || !this.ws || this.ws.readyState !== WebSocket.OPEN) return;

    this.inputEl.value = '';
    this.inputEl.style.height = 'auto';
    this.processing = true;
    this.stopBtn.style.display = '';

    this.chatHistory.push({ role: 'user', content: text });
    await this.appendMessage('user', text, new Date());
    this.scrollToBottom();

    // Mark the start of the request so we can attribute the eventual
    // response duration to the assistant footer ("HH:MM · 1.4s · ...").
    this.sendStartedAt = Date.now();

    // Spec F: send only new message content — server loads history from DB.
    // The chatHistory array is kept locally for instant rendering but is no
    // longer the source of truth.
    this.ws.send(JSON.stringify({
      type: 'message',
      content: text,
    }));
  }

  private async onWsMessage(msg: WsMessage): Promise<void> {
    msg = normalizeFrame(msg); // channel → type shim (Spec F migration)
    switch (msg.type) {
      case 'token':
        // First token after a thinking block — collapse the thinking block
        // and stamp it with its duration. The reasoning text stays visible
        // for the user to re-open later, it's just no longer the active
        // streaming target. We do NOT push the thinking content into
        // chatHistory, so it never gets sent back to the LLM on follow-ups.
        if (this.thinkingEl) {
          this.renderer.finalizeThinking(this.thinkingEl);
          this.thinkingEl = null;
        }
        // Streaming token — append to streaming element
        if (!this.streamingEl) {
          this.removeStatusMsg();
          this.streamingEl = this.renderer.createStreamingMessage('assistant');
          this.messagesEl.appendChild(this.streamingEl);
        }
        this.renderer.appendToken(this.streamingEl, msg.content);
        this.scrollToBottom();
        break;

      case 'thinking':
        // Thinking/reasoning — collapsible section
        if (!this.thinkingEl) {
          this.thinkingEl = this.renderer.renderThinking(msg.content);
          this.messagesEl.appendChild(this.thinkingEl);
        } else {
          this.renderer.appendThinking(this.thinkingEl, msg.content);
        }
        this.scrollToBottom();
        break;

      case 'tool_call':
        // First tool call after thinking — collapse the reasoning block.
        if (this.thinkingEl) {
          this.renderer.finalizeThinking(this.thinkingEl);
          this.thinkingEl = null;
        }
        this.chatHistory.push({
          role: 'assistant',
          content: msg.content || '',
          tool_calls: msg.tool_calls,
        });
        if (Array.isArray(msg.tool_calls)) {
          this.toolCallsEl = this.renderer.renderToolCall(msg.tool_calls);
          this.messagesEl.appendChild(this.toolCallsEl);
          this.scrollToBottom();
        }
        break;

      case 'tool_result':
        this.chatHistory.push({
          role: 'tool',
          content: msg.content,
          tool_call_id: msg.tool_call_id,
        });
        if (this.toolCallsEl && msg.tool_call_id) {
          this.renderer.setToolResult(this.toolCallsEl, msg.tool_call_id, msg.content);
        }
        break;

      case 'response': {
        const responseText = typeof msg.content === 'string' ? msg.content : '';
        const durationMs = this.sendStartedAt ? Date.now() - this.sendStartedAt : undefined;
        this.sendStartedAt = null;

        // Final response → close any open thinking block (with elapsed stamp).
        if (this.thinkingEl) {
          this.renderer.finalizeThinking(this.thinkingEl);
          this.thinkingEl = null;
        }

        this.chatHistory.push({ role: 'assistant', content: responseText });

        // If we were streaming, finalize. Otherwise render full message.
        if (this.streamingEl) {
          await this.renderer.finalizeStreaming(this.streamingEl, msg.usage, durationMs);
          this.streamingEl = null;
        } else {
          const label = msg.agent_name || 'assistant';
          // Empty/missing content can happen if the LLM stream produced
          // no token chunks (e.g. tool-only turn or upstream parse issue);
          // show a placeholder instead of literal "undefined".
          const display = responseText || '_(no response text)_';
          await this.appendMessage(label, display, new Date(), undefined, {
            usage: msg.usage,
            durationMs,
          });
        }
        this.toolCallsEl = null;
        this.processing = false;
        this.stopBtn.style.display = 'none';
        this.removeStatusMsg();
        this.scrollToBottom();
        break;
      }

      case 'summary':
        if (msg.summary_of) {
          const block = this.renderer.renderSummaryBlock(
            msg.summary_of.length,
            msg.content,
          );
          this.messagesEl.appendChild(block);
          this.scrollToBottom();
        }
        break;

      case 'status':
        if (msg.content === 'cancelled') {
          this.finalizeStream();
          this.processing = false;
          this.stopBtn.style.display = 'none';
          this.removeStatusMsg();
          await this.appendMessage('system', 'Cancelled by user');
          this.scrollToBottom();
        } else {
          this.showStatusMsg(msg.content);
        }
        break;

      case 'title_update':
        this.title.label = msg.content;
        if (this.onTitleChange) {
          this.onTitleChange(msg.content);
        }
        break;

      case 'error':
        this.finalizeStream();
        this.processing = false;
        this.stopBtn.style.display = 'none';
        this.removeStatusMsg();
        await this.appendMessage('system', `Error: ${msg.content}`);
        this.scrollToBottom();
        break;
    }
  }

  private finalizeStream(): void {
    if (this.streamingEl) {
      this.renderer.finalizeStreaming(this.streamingEl);
      this.streamingEl = null;
    }
    if (this.thinkingEl) {
      // Stop the live ticker even on cancel/error so it doesn't keep
      // counting forever inside an orphaned details block.
      this.renderer.finalizeThinking(this.thinkingEl);
      this.thinkingEl = null;
    }
    this.toolCallsEl = null;
    this.sendStartedAt = null;
  }

  private async appendMessage(
    role: string,
    content: string,
    timestamp?: string | Date | number,
    messageId?: string,
    meta?: { durationMs?: number; usage?: { tokens_in: number; tokens_out: number; model: string } },
  ): Promise<HTMLElement> {
    const el = await this.renderer.render(role, content, timestamp, meta);
    if (messageId) {
      el.dataset.messageId = messageId;
    }
    this.messagesEl.appendChild(el);
    return el;
  }

  private addMessageContextMenu(el: HTMLElement, messageId: string): void {
    el.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      this.renderer.createMessageContextMenu(e, messageId, {
        onBranch: (mid) => this.handleBranch(mid),
        onBranchWithSummary: (mid) => this.handleBranchWithSummary(mid),
        onSummarizeAbove: (mid) => this.handleSummarize(mid),
        canBranch: true, // TODO: check depth
      });
    });
  }

  private async handleBranch(messageId: string): Promise<void> {
    try {
      const result = await branchConversation({
        parent_id: this.conversationId,
        branch_point_message_id: messageId,
      });
      if (this.onBranch) {
        this.onBranch(result.id, result.title);
      }
    } catch (err: any) {
      await this.appendMessage('system', `Branch failed: ${err.message}`);
    }
  }

  private async handleBranchWithSummary(messageId: string): Promise<void> {
    try {
      const result = await branchConversation({
        parent_id: this.conversationId,
        branch_point_message_id: messageId,
        title: 'Thread with summary',
      });
      if (this.onBranch) {
        this.onBranch(result.id, result.title);
      }
    } catch (err: any) {
      await this.appendMessage('system', `Branch failed: ${err.message}`);
    }
  }

  private async handleSummarize(messageId: string): Promise<void> {
    try {
      this.showStatusMsg('Summarizing...');
      await summarizeMessages(this.conversationId, messageId);
      this.removeStatusMsg();
      // Refresh messages to show summary block
      this.messagesEl.innerHTML = '';
      this.chatHistory = [this.chatHistory[0]]; // keep system prompt
      this.messageIds = [];
      await this.loadInitialMessages();
    } catch (err: any) {
      this.removeStatusMsg();
      await this.appendMessage('system', `Summarization failed: ${err.message}`);
    }
  }

  private showStatusMsg(content: string): void {
    this.removeStatusMsg();
    this.statusElement = this.renderer.renderStatus(content);
    this.messagesEl.appendChild(this.statusElement);
    this.scrollToBottom();
  }

  private removeStatusMsg(): void {
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
    let cssStatus = status;
    if (status.startsWith('reconnecting')) cssStatus = 'reconnecting';
    this.statusEl.className = `hub-chat-status-bar hub-chat-status-bar--${cssStatus}`;
  }
}
