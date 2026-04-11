/**
 * ConversationTree — renders conversations as a tree with branches as child nodes.
 * Supports folders as top-level sections and branches up to 3 levels deep.
 */
import { Conversation } from '../api.js';

type OpenCallback = (conversationId: string, title: string) => void;
type ContextMenuCallback = (e: MouseEvent, conv: Conversation) => void;

export class ConversationTree {
  private container: HTMLDivElement;
  private onOpen: OpenCallback;
  private onContextMenu: ContextMenuCallback;

  constructor(
    container: HTMLDivElement,
    onOpen: OpenCallback,
    onContextMenu: ContextMenuCallback,
  ) {
    this.container = container;
    this.onOpen = onOpen;
    this.onContextMenu = onContextMenu;
  }

  render(conversations: Conversation[]): void {
    this.container.innerHTML = '';

    if (conversations.length === 0) {
      this.container.innerHTML = '<div class="hub-chat-sidebar-empty">No conversations yet</div>';
      return;
    }

    // Build tree structure
    const byId = new Map<string, Conversation>();
    const children = new Map<string, Conversation[]>();
    const roots: Conversation[] = [];

    for (const conv of conversations) {
      byId.set(conv.id, conv);
      if (conv.parent_id) {
        if (!children.has(conv.parent_id)) children.set(conv.parent_id, []);
        children.get(conv.parent_id)!.push(conv);
      } else {
        roots.push(conv);
      }
    }

    // Group roots by folder
    const grouped = new Map<string, Conversation[]>();
    for (const conv of roots) {
      const key = conv.folder || '';
      if (!grouped.has(key)) grouped.set(key, []);
      grouped.get(key)!.push(conv);
    }

    // Render ungrouped first
    const ungrouped = grouped.get('') || [];
    for (const conv of ungrouped) {
      this.renderNode(conv, children, 0);
    }

    // Render folders
    for (const [folder, convs] of grouped) {
      if (folder === '') continue;
      const folderEl = document.createElement('div');
      folderEl.className = 'hub-chat-sidebar-folder';
      folderEl.innerHTML = `<div class="hub-chat-sidebar-folder-name">${esc(folder)}</div>`;
      this.container.appendChild(folderEl);
      for (const conv of convs) {
        this.renderNode(conv, children, 0, folderEl);
      }
    }
  }

  private renderNode(
    conv: Conversation,
    children: Map<string, Conversation[]>,
    depth: number,
    parent?: HTMLElement,
  ): void {
    const target = parent || this.container;
    const el = document.createElement('div');
    el.className = 'hub-chat-sidebar-item';
    if (depth > 0) {
      el.classList.add('hub-chat-sidebar-item--branch');
      el.style.paddingLeft = `${12 + depth * 16}px`;
    }
    el.dataset.id = conv.id;

    const modeIcon = conv.mode === 'agent' ? '🤖' : conv.mode === 'tools' ? '🔧' : '💬';
    const branchIcon = depth > 0 ? '└ ' : '';
    const label = conv.branch_label || conv.title;

    el.innerHTML = `
      <span class="hub-chat-sidebar-item-icon">${branchIcon}${modeIcon}</span>
      <span class="hub-chat-sidebar-item-title">${esc(label)}</span>
    `;

    el.addEventListener('click', () => this.onOpen(conv.id, conv.title));
    el.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      this.onContextMenu(e, conv);
    });

    target.appendChild(el);

    // Render children (branches)
    const kids = children.get(conv.id);
    if (kids && depth < 3) {
      for (const child of kids) {
        this.renderNode(child, children, depth + 1, target);
      }
    }
  }
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
