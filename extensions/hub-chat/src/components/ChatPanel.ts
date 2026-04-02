/**
 * Hub Chat Panel — WebSocket-based real-time chat with agent.
 */
import { Widget } from '@lumino/widgets';
import { ServerConnection } from '@jupyterlab/services';

interface ChatMessage {
  type: 'message' | 'response' | 'error' | 'status';
  content: string;
}

interface DisplayMessage {
  role: 'user' | 'agent';
  content: string;
  timestamp: Date;
}

export class ChatPanelWidget extends Widget {
  private ws: WebSocket | null = null;
  private messages: DisplayMessage[] = [];
  private status: string = 'disconnected';

  private messagesEl: HTMLDivElement;
  private inputEl: HTMLTextAreaElement;
  private sendBtn: HTMLButtonElement;
  private statusEl: HTMLDivElement;

  constructor() {
    super();
    this.id = 'hub-chat-panel';
    this.title.label = 'Hub Chat';
    this.title.closable = true;
    this.addClass('hub-chat-panel');

    // Messages area
    this.messagesEl = document.createElement('div');
    this.messagesEl.className = 'hub-chat-messages';
    this.node.appendChild(this.messagesEl);

    // Input area
    const inputArea = document.createElement('div');
    inputArea.className = 'hub-chat-input-area';

    this.inputEl = document.createElement('textarea');
    this.inputEl.className = 'hub-chat-input';
    this.inputEl.placeholder = 'Ask a question about your data...';
    this.inputEl.rows = 1;
    this.inputEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.sendMessage();
      }
    });
    this.inputEl.addEventListener('input', () => {
      this.inputEl.style.height = 'auto';
      this.inputEl.style.height = Math.min(this.inputEl.scrollHeight, 120) + 'px';
    });
    inputArea.appendChild(this.inputEl);

    this.sendBtn = document.createElement('button');
    this.sendBtn.className = 'hub-chat-send-btn';
    this.sendBtn.textContent = 'Send';
    this.sendBtn.addEventListener('click', () => this.sendMessage());
    inputArea.appendChild(this.sendBtn);

    this.node.appendChild(inputArea);

    // Status bar
    this.statusEl = document.createElement('div');
    this.statusEl.className = 'hub-chat-status';
    this.node.appendChild(this.statusEl);

    this.updateStatus('disconnected');
  }

  onAfterShow(): void {
    if (!this.ws) {
      this.connect();
    }
  }

  onBeforeHide(): void {
    // Keep connection alive when hidden
  }

  dispose(): void {
    this.disconnect();
    super.dispose();
  }

  private connect(): void {
    const settings = ServerConnection.makeSettings();
    // Hub Service WebSocket URL — proxied through JupyterHub
    const baseUrl = settings.baseUrl.replace(/^http/, 'ws');
    // User ID will be extracted from the session
    const wsUrl = `${baseUrl}hugr/ws/current`;

    this.updateStatus('connecting');

    try {
      this.ws = new WebSocket(wsUrl);
    } catch {
      this.updateStatus('error');
      return;
    }

    this.ws.onopen = () => {
      this.updateStatus('connected');
    };

    this.ws.onmessage = (event) => {
      const msg: ChatMessage = JSON.parse(event.data);
      this.handleMessage(msg);
    };

    this.ws.onclose = () => {
      this.ws = null;
      this.updateStatus('disconnected');
      // Auto-reconnect after 3s
      setTimeout(() => {
        if (!this.isDisposed) {
          this.connect();
        }
      }, 3000);
    };

    this.ws.onerror = () => {
      this.updateStatus('error');
    };
  }

  private disconnect(): void {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  private sendMessage(): void {
    const text = this.inputEl.value.trim();
    if (!text) return;
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      this.updateStatus('error');
      return;
    }

    // Show user message
    this.addMessage({ role: 'user', content: text, timestamp: new Date() });

    // Send to WebSocket
    const msg: ChatMessage = { type: 'message', content: text };
    this.ws.send(JSON.stringify(msg));

    // Clear input
    this.inputEl.value = '';
    this.inputEl.style.height = 'auto';
    this.sendBtn.disabled = true;
  }

  private handleMessage(msg: ChatMessage): void {
    switch (msg.type) {
      case 'response':
        this.addMessage({ role: 'agent', content: msg.content, timestamp: new Date() });
        this.sendBtn.disabled = false;
        this.updateStatus('connected');
        break;
      case 'status':
        this.updateStatus(msg.content);
        break;
      case 'error':
        this.addMessage({ role: 'agent', content: `Error: ${msg.content}`, timestamp: new Date() });
        this.sendBtn.disabled = false;
        this.updateStatus('error');
        break;
    }
  }

  private addMessage(msg: DisplayMessage): void {
    this.messages.push(msg);

    const el = document.createElement('div');
    el.className = `hub-chat-message hub-chat-message--${msg.role}`;

    const header = document.createElement('div');
    header.className = 'hub-chat-message-header';
    header.textContent = `${msg.role === 'user' ? 'You' : 'Agent'} · ${formatTime(msg.timestamp)}`;
    el.appendChild(header);

    const body = document.createElement('div');
    body.className = 'hub-chat-message-body';

    // Try to render rich content (data tables)
    if (msg.role === 'agent' && isDataResponse(msg.content)) {
      body.appendChild(renderDataTable(msg.content));
    } else {
      body.textContent = msg.content;
    }

    el.appendChild(body);
    this.messagesEl.appendChild(el);
    this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
  }

  private updateStatus(status: string): void {
    this.status = status;
    this.statusEl.textContent = statusLabel(status);
    this.statusEl.className = 'hub-chat-status';

    switch (status) {
      case 'connected':
        this.statusEl.classList.add('hub-chat-status--connected');
        break;
      case 'thinking':
        this.statusEl.classList.add('hub-chat-status--thinking');
        break;
      case 'error':
        this.statusEl.classList.add('hub-chat-status--error');
        break;
    }
  }
}

// ── Helpers ────────────────────────────────────────────────────────────

function formatTime(d: Date): string {
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

function statusLabel(s: string): string {
  switch (s) {
    case 'connected': return 'Connected';
    case 'connecting': return 'Connecting...';
    case 'disconnected': return 'Disconnected — reconnecting...';
    case 'thinking': return 'Agent is thinking...';
    case 'error': return 'Connection error';
    default: return s;
  }
}

/**
 * Check if content looks like a JSON data response (array of objects).
 */
function isDataResponse(content: string): boolean {
  const trimmed = content.trim();
  if (!trimmed.startsWith('[')) return false;
  try {
    const data = JSON.parse(trimmed);
    return Array.isArray(data) && data.length > 0 && typeof data[0] === 'object';
  } catch {
    return false;
  }
}

/**
 * Render a JSON array of objects as a sortable HTML table.
 */
function renderDataTable(content: string): HTMLElement {
  const data: Record<string, unknown>[] = JSON.parse(content.trim());
  const keys = Object.keys(data[0]);

  const table = document.createElement('table');
  table.className = 'hub-chat-data-table';

  // Header
  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const key of keys) {
    const th = document.createElement('th');
    th.textContent = key;
    let asc = true;
    th.addEventListener('click', () => {
      data.sort((a, b) => {
        const va = a[key];
        const vb = b[key];
        if (va == null) return 1;
        if (vb == null) return -1;
        const cmp = String(va).localeCompare(String(vb), undefined, { numeric: true });
        return asc ? cmp : -cmp;
      });
      asc = !asc;
      renderBody();
    });
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  // Body
  const tbody = document.createElement('tbody');
  table.appendChild(tbody);

  function renderBody() {
    tbody.innerHTML = '';
    for (const row of data) {
      const tr = document.createElement('tr');
      for (const key of keys) {
        const td = document.createElement('td');
        const val = row[key];
        td.textContent = val == null ? '' : String(val);
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    }
  }
  renderBody();

  return table;
}
