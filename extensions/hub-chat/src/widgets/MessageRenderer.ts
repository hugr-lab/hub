/**
 * MessageRenderer — renders chat messages as markdown with syntax highlighting.
 * Supports streaming tokens, collapsible thinking/tool sections, and summary blocks.
 */
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';
import type { WsMessage } from '../api.js';

/**
 * Normalize a WebSocket frame: if the new `channel` field is present but
 * `type` is missing, derive `type` from `channel` so existing rendering
 * logic works unchanged. This shim will be removed when the renderer is
 * rewritten for channels (Spec H).
 */
export function normalizeFrame(msg: WsMessage): WsMessage {
  if (msg.channel && !msg.type) {
    switch (msg.channel) {
      case 'final':       msg.type = msg.content ? 'response' : 'token'; break;
      case 'analysis':    msg.type = 'thinking'; break;
      case 'tool_call':   msg.type = 'tool_call'; break;
      case 'tool_result': msg.type = 'tool_result'; break;
      case 'status':      msg.type = 'status'; break;
      case 'error':       msg.type = 'error'; break;
      default:            msg.type = 'status'; break;
    }
  }
  return msg;
}

export class MessageRenderer {
  constructor(private rendermime: IRenderMimeRegistry) {}

  /**
   * Render a message as HTML. Returns an HTMLElement ready for insertion.
   * Always shows a per-message footer with the wall-clock time and (when
   * provided) duration / token usage.
   */
  async render(
    role: string,
    content: string,
    timestamp?: string | Date | number,
    meta?: MessageMeta,
  ): Promise<HTMLElement> {
    const wrapper = document.createElement('div');
    const cssRole = role === 'user' ? 'user' : 'assistant';
    wrapper.className = `hub-chat-message hub-chat-message--${cssRole}`;

    // Role label only — timestamp moves to the per-message footer.
    const header = document.createElement('div');
    header.className = 'hub-chat-message-header';
    const roleLabel = document.createElement('span');
    roleLabel.className = 'hub-chat-message-role';
    roleLabel.textContent = role === 'user' ? 'You' : role.charAt(0).toUpperCase() + role.slice(1);
    header.appendChild(roleLabel);
    wrapper.appendChild(header);

    // Content — render as markdown for assistant, plain for user
    const body = document.createElement('div');
    body.className = 'hub-chat-message-body';

    if (role !== 'user') {
      await this.renderMarkdown(body, content);
    } else {
      body.textContent = content;
    }

    wrapper.appendChild(body);
    wrapper.appendChild(buildMessageFooter(timestamp, meta));
    return wrapper;
  }

  /**
   * Create a streaming message element that can be updated with token chunks.
   * Includes a live "Streaming… (Xs)" footer that ticks every 500ms while
   * tokens arrive; `finalizeStreaming` stops the ticker and replaces the
   * footer with the final time / duration / tokens line.
   */
  createStreamingMessage(role: string): HTMLElement {
    const wrapper = document.createElement('div');
    wrapper.className = `hub-chat-message hub-chat-message--assistant hub-chat-message--streaming`;

    const header = document.createElement('div');
    header.className = 'hub-chat-message-header';
    const roleLabel = document.createElement('span');
    roleLabel.className = 'hub-chat-message-role';
    roleLabel.textContent = role.charAt(0).toUpperCase() + role.slice(1);
    header.appendChild(roleLabel);
    wrapper.appendChild(header);

    const body = document.createElement('div');
    body.className = 'hub-chat-message-body';
    body.dataset.streaming = 'true';
    wrapper.appendChild(body);

    const startedAt = Date.now();
    wrapper.dataset.startedAt = String(startedAt);
    const liveFooter = document.createElement('div');
    liveFooter.className = 'hub-chat-message-footer hub-chat-message-footer--live';
    liveFooter.textContent = 'Streaming… (0s)';
    wrapper.appendChild(liveFooter);
    const tickerId = window.setInterval(() => {
      const elapsed = (Date.now() - startedAt) / 1000;
      liveFooter.textContent = `Streaming… (${formatElapsed(elapsed)})`;
    }, 500);
    wrapper.dataset.tickerId = String(tickerId);

    return wrapper;
  }

  /**
   * Append a token chunk to a streaming message element.
   */
  appendToken(el: HTMLElement, token: string): void {
    const body = el.querySelector('.hub-chat-message-body');
    if (!body) return;
    // Append to raw text accumulator
    const current = body.getAttribute('data-raw') || '';
    body.setAttribute('data-raw', current + token);
    body.textContent = current + token;
  }

  /**
   * Finalize a streaming message — stop the live ticker, render the
   * accumulated text as markdown, and replace the live footer with the
   * final timestamp · duration · tokens line. If `durationMs` isn't passed
   * we derive it from the element's `startedAt` dataset.
   */
  async finalizeStreaming(
    el: HTMLElement,
    usage?: { tokens_in: number; tokens_out: number; model: string },
    durationMs?: number,
  ): Promise<void> {
    // Stop live ticker.
    const tickerId = Number(el.dataset.tickerId || 0);
    if (tickerId > 0) {
      window.clearInterval(tickerId);
      delete el.dataset.tickerId;
    }
    if (durationMs === undefined) {
      const startedAt = Number(el.dataset.startedAt || 0);
      if (startedAt > 0) durationMs = Date.now() - startedAt;
    }
    delete el.dataset.startedAt;

    el.classList.remove('hub-chat-message--streaming');
    const body = el.querySelector('.hub-chat-message-body');
    if (!body) return;
    const raw = body.getAttribute('data-raw') || body.textContent || '';
    body.removeAttribute('data-raw');
    body.removeAttribute('data-streaming');
    body.innerHTML = '';
    await this.renderMarkdown(body as HTMLElement, raw);

    // Replace the live footer (if any) with the final stats line.
    const liveFooter = el.querySelector('.hub-chat-message-footer--live');
    if (liveFooter) liveFooter.remove();
    el.appendChild(buildMessageFooter(new Date(), { usage, durationMs }));
  }

  /**
   * Render a collapsible thinking section. Open by default while streaming
   * with a live "Thinking… (Xs)" ticker that updates every second; call
   * `finalizeThinking` once the LLM produces its first content delta or the
   * response finishes — that stops the ticker and stamps the final "Thought
   * (X.Xs)" label.
   */
  renderThinking(content: string): HTMLElement {
    const details = document.createElement('details');
    details.className = 'hub-chat-thinking hub-chat-thinking--streaming';
    details.open = true;
    const startedAt = Date.now();
    details.dataset.startedAt = String(startedAt);
    const summary = document.createElement('summary');
    summary.textContent = 'Thinking… (0s)';
    details.appendChild(summary);
    const body = document.createElement('div');
    body.className = 'hub-chat-thinking-body';
    body.textContent = content;
    details.appendChild(body);

    // Live elapsed-time ticker. Stored on the element via setInterval id so
    // finalizeThinking can clear it. We tick every 500ms so the UI feels
    // live without spamming the layout.
    const tickerId = window.setInterval(() => {
      const elapsed = (Date.now() - startedAt) / 1000;
      summary.textContent = `Thinking… (${formatElapsed(elapsed)})`;
    }, 500);
    details.dataset.tickerId = String(tickerId);

    return details;
  }

  /**
   * Append thinking text to existing thinking element.
   */
  appendThinking(el: HTMLElement, text: string): void {
    const body = el.querySelector('.hub-chat-thinking-body');
    if (body) {
      body.textContent = (body.textContent || '') + text;
    }
  }

  /**
   * Mark a thinking section as complete: stop the ticker, collapse the
   * details, and replace "Thinking…" with "Thought (X.Xs)". The body is
   * kept so the user can re-open and re-read the reasoning at any point.
   */
  finalizeThinking(el: HTMLElement): void {
    const tickerId = Number(el.dataset.tickerId || 0);
    if (tickerId > 0) {
      window.clearInterval(tickerId);
      delete el.dataset.tickerId;
    }
    el.classList.remove('hub-chat-thinking--streaming');
    el.classList.add('hub-chat-thinking--done');
    (el as HTMLDetailsElement).open = false;
    const summary = el.querySelector('summary');
    if (summary) {
      const startedAt = Number(el.dataset.startedAt || 0);
      const elapsed = startedAt > 0 ? (Date.now() - startedAt) / 1000 : 0;
      summary.textContent = elapsed > 0 ? `Thought (${formatElapsed(elapsed)})` : 'Thought';
    }
  }

  /**
   * Render a collapsible tool call section.
   */
  renderToolCall(toolCalls: any[]): HTMLElement {
    const container = document.createElement('div');
    container.className = 'hub-chat-tool-calls';
    for (const tc of toolCalls) {
      const details = document.createElement('details');
      details.className = 'hub-chat-tool-call';
      const summary = document.createElement('summary');
      summary.innerHTML = `<code>${escapeHtml(tc.name || 'unknown')}</code>`;
      details.appendChild(summary);
      const argsEl = document.createElement('pre');
      argsEl.className = 'hub-chat-tool-call-args';
      argsEl.textContent = JSON.stringify(tc.arguments || {}, null, 2);
      details.appendChild(argsEl);
      // Placeholder for result
      const resultEl = document.createElement('div');
      resultEl.className = 'hub-chat-tool-result';
      resultEl.dataset.toolCallId = tc.id || '';
      details.appendChild(resultEl);
      container.appendChild(details);
    }
    return container;
  }

  /**
   * Set tool result in a tool call section.
   */
  setToolResult(container: HTMLElement, toolCallId: string, result: string): void {
    const resultEl = container.querySelector(`.hub-chat-tool-result[data-tool-call-id="${toolCallId}"]`);
    if (resultEl) {
      resultEl.textContent = result;
      resultEl.classList.add('hub-chat-tool-result--filled');
    }
  }

  /**
   * Render a collapsed summary block.
   */
  renderSummaryBlock(messageCount: number, summaryText: string, onExpand?: () => void): HTMLElement {
    const wrapper = document.createElement('div');
    wrapper.className = 'hub-chat-summary-block';

    const badge = document.createElement('div');
    badge.className = 'hub-chat-summary-badge';
    badge.textContent = `┄ ${messageCount} messages summarized ┄`;
    wrapper.appendChild(badge);

    const summaryEl = document.createElement('div');
    summaryEl.className = 'hub-chat-summary-text';
    summaryEl.textContent = summaryText;
    wrapper.appendChild(summaryEl);

    if (onExpand) {
      const expandBtn = document.createElement('button');
      expandBtn.className = 'hub-chat-summary-expand';
      expandBtn.textContent = 'Show original messages';
      expandBtn.addEventListener('click', () => {
        expandBtn.remove();
        onExpand();
      });
      wrapper.appendChild(expandBtn);
    }

    return wrapper;
  }

  /**
   * Render a status message (thinking, tool call, etc.).
   */
  renderStatus(content: string): HTMLElement {
    const el = document.createElement('div');
    el.className = 'hub-chat-status';

    if (content === 'thinking') {
      el.innerHTML = '<span class="hub-chat-status-dot"></span> Thinking...';
    } else if (content.startsWith('tool:')) {
      const toolName = content.substring(5);
      el.innerHTML = `<span class="hub-chat-status-dot"></span> Calling <code>${escapeHtml(toolName)}</code>...`;
    } else if (content.startsWith('agent offline') || content.startsWith('Agent not connected')) {
      el.innerHTML = `<span class="hub-chat-status-warn"></span> ${escapeHtml(content)}`;
    } else {
      el.textContent = content;
    }

    return el;
  }

  /**
   * Create a context menu for a message (branch, summarize).
   */
  createMessageContextMenu(
    e: MouseEvent,
    messageId: string,
    options: {
      onBranch?: (messageId: string) => void;
      onBranchWithSummary?: (messageId: string) => void;
      onSummarizeAbove?: (messageId: string) => void;
      canBranch?: boolean;
    },
  ): void {
    document.querySelectorAll('.hub-chat-ctx-menu').forEach(m => m.remove());

    const menu = document.createElement('div');
    menu.className = 'hub-chat-ctx-menu';
    menu.style.left = e.pageX + 'px';
    menu.style.top = e.pageY + 'px';

    if (options.onBranch && options.canBranch !== false) {
      const branch = document.createElement('div');
      branch.className = 'hub-chat-ctx-menu-item';
      branch.textContent = 'Start new thread';
      branch.addEventListener('click', () => { menu.remove(); options.onBranch!(messageId); });
      menu.appendChild(branch);
    }

    if (options.onBranchWithSummary && options.canBranch !== false) {
      const branchSum = document.createElement('div');
      branchSum.className = 'hub-chat-ctx-menu-item';
      branchSum.textContent = 'Start thread with summary';
      branchSum.addEventListener('click', () => { menu.remove(); options.onBranchWithSummary!(messageId); });
      menu.appendChild(branchSum);
    }

    if (options.canBranch === false) {
      const disabled = document.createElement('div');
      disabled.className = 'hub-chat-ctx-menu-item hub-chat-ctx-menu-item--disabled';
      disabled.textContent = 'Maximum branch depth reached (3)';
      menu.appendChild(disabled);
    }

    if (options.onSummarizeAbove) {
      const summarize = document.createElement('div');
      summarize.className = 'hub-chat-ctx-menu-item';
      summarize.textContent = 'Summarize above';
      summarize.addEventListener('click', () => { menu.remove(); options.onSummarizeAbove!(messageId); });
      menu.appendChild(summarize);
    }

    document.body.appendChild(menu);
    const close = () => { menu.remove(); document.removeEventListener('click', close); };
    setTimeout(() => document.addEventListener('click', close), 0);
  }

  private async renderMarkdown(container: HTMLElement, markdown: string): Promise<void> {
    try {
      const renderer = this.rendermime.createRenderer('text/markdown');
      const model = this.rendermime.createModel({
        data: { 'text/markdown': markdown },
      });
      await renderer.renderModel(model);
      container.appendChild(renderer.node);
    } catch {
      // Fallback: plain text
      container.textContent = markdown;
    }
  }
}

/**
 * Per-message metadata shown in the footer (timestamp / duration / tokens).
 * All fields are optional — `render()` and `finalizeStreaming()` pass whatever
 * they have. The footer is always rendered with at least the wall-clock time.
 */
export interface MessageMeta {
  /** End-to-end duration from user send to final response, in milliseconds. */
  durationMs?: number;
  /** Token usage and model name (assistant messages only). */
  usage?: { tokens_in: number; tokens_out: number; model: string };
}

/**
 * Build the footer that sits under every chat message.
 *
 * Format: `HH:MM · 1.4s · gemma-4-26b · 12→128 tokens`
 *
 * Only the timestamp is mandatory; the other segments appear when their
 * corresponding metadata field is present, so user messages and incomplete
 * assistant messages still get a clean time stamp.
 */
function buildMessageFooter(
  timestamp?: string | Date | number | null,
  meta?: MessageMeta,
): HTMLElement {
  const footer = document.createElement('div');
  footer.className = 'hub-chat-message-footer';

  const parts: string[] = [];
  parts.push(formatTime(timestamp ?? new Date()));
  if (meta?.durationMs && meta.durationMs > 0) {
    parts.push(formatDuration(meta.durationMs));
  }
  if (meta?.usage) {
    if (meta.usage.model) parts.push(meta.usage.model);
    if (meta.usage.tokens_in || meta.usage.tokens_out) {
      parts.push(`${meta.usage.tokens_in}→${meta.usage.tokens_out} tokens`);
    }
  }
  footer.textContent = parts.filter(Boolean).join(' · ');
  return footer;
}

function formatTime(value: string | Date | number): string {
  try {
    const d = value instanceof Date ? value : new Date(value as any);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return formatElapsed(ms / 1000);
}

/**
 * Format an elapsed-seconds value for the live ticker. Sub-minute values
 * stay in seconds (whole when ≥10s, one decimal otherwise) so the UI
 * doesn't twitch while updating; longer waits switch to "Mm SSs".
 */
function formatElapsed(seconds: number): string {
  if (seconds < 10) return `${seconds.toFixed(1)}s`;
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const m = Math.floor(seconds / 60);
  const s = Math.round(seconds - m * 60);
  return `${m}m${s.toString().padStart(2, '0')}s`;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
