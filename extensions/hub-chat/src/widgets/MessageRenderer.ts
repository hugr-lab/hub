/**
 * MessageRenderer — renders chat messages as markdown with syntax highlighting.
 * Uses JupyterLab's IRenderMimeRegistry for consistent styling.
 */
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';
import { Widget } from '@lumino/widgets';

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
    } else if (content.startsWith('agent offline')) {
      el.innerHTML = `<span class="hub-chat-status-warn"></span> ${escapeHtml(content)}`;
    } else {
      el.textContent = content;
    }

    return el;
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
