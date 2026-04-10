/**
 * MessageRenderer — renders chat messages as markdown with syntax highlighting.
 * Supports streaming tokens, collapsible thinking/tool sections, and summary blocks.
 */
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';

export class MessageRenderer {
  constructor(private rendermime: IRenderMimeRegistry) {}

  /**
   * Render a message as HTML. Returns an HTMLElement ready for insertion.
   */
  async render(role: string, content: string, timestamp?: string): Promise<HTMLElement> {
    const wrapper = document.createElement('div');
    const cssRole = role === 'user' ? 'user' : 'assistant';
    wrapper.className = `hub-chat-message hub-chat-message--${cssRole}`;

    // Role label
    const header = document.createElement('div');
    header.className = 'hub-chat-message-header';
    const roleLabel = document.createElement('span');
    roleLabel.className = 'hub-chat-message-role';
    roleLabel.textContent = role === 'user' ? 'You' : role.charAt(0).toUpperCase() + role.slice(1);
    header.appendChild(roleLabel);

    if (timestamp) {
      const time = document.createElement('span');
      time.className = 'hub-chat-message-time';
      time.textContent = formatTime(timestamp);
      header.appendChild(time);
    }
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
    return wrapper;
  }

  /**
   * Create a streaming message element that can be updated with token chunks.
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
   * Finalize a streaming message — render as markdown and add usage footer.
   */
  async finalizeStreaming(el: HTMLElement, usage?: { tokens_in: number; tokens_out: number; model: string }): Promise<void> {
    el.classList.remove('hub-chat-message--streaming');
    const body = el.querySelector('.hub-chat-message-body');
    if (!body) return;
    const raw = body.getAttribute('data-raw') || body.textContent || '';
    body.removeAttribute('data-raw');
    body.removeAttribute('data-streaming');
    body.innerHTML = '';
    await this.renderMarkdown(body as HTMLElement, raw);

    if (usage) {
      const footer = document.createElement('div');
      footer.className = 'hub-chat-message-usage';
      footer.textContent = `${usage.model} · ${usage.tokens_in}→${usage.tokens_out} tokens`;
      el.appendChild(footer);
    }
  }

  /**
   * Render a collapsible thinking section.
   */
  renderThinking(content: string): HTMLElement {
    const details = document.createElement('details');
    details.className = 'hub-chat-thinking';
    const summary = document.createElement('summary');
    summary.textContent = 'Thinking...';
    details.appendChild(summary);
    const body = document.createElement('div');
    body.className = 'hub-chat-thinking-body';
    body.textContent = content;
    details.appendChild(body);
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

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
