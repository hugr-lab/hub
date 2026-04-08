/**
 * ChatSidebar — conversation tree in left sidebar.
 * Lists conversations grouped by mode/folder.
 * Clicking opens conversation as main area tab.
 */
import { Widget } from '@lumino/widgets';
import {
  listConversations, createConversation, renameConversation, deleteConversation,
  Conversation,
} from '../api.js';

type OpenCallback = (conversationId: string, title: string) => void;

export class ChatSidebarWidget extends Widget {
  private onOpen: OpenCallback;
  private conversations: Conversation[] = [];
  private listEl: HTMLDivElement;

  constructor(onOpen: OpenCallback) {
    super();
    this.onOpen = onOpen;
    this.id = 'hub-chat-sidebar';
    this.title.label = 'Chats';
    this.addClass('hub-chat-sidebar');

    // Header with New Chat button
    const header = document.createElement('div');
    header.className = 'hub-chat-sidebar-header';

    const title = document.createElement('span');
    title.textContent = 'Conversations';
    title.className = 'hub-chat-sidebar-title';
    header.appendChild(title);

    const newBtn = document.createElement('button');
    newBtn.className = 'hub-chat-sidebar-new-btn';
    newBtn.textContent = '+';
    newBtn.title = 'New Chat';
    newBtn.addEventListener('click', () => this.showNewChatDialog());
    header.appendChild(newBtn);

    this.node.appendChild(header);

    // Conversation list
    this.listEl = document.createElement('div');
    this.listEl.className = 'hub-chat-sidebar-list';
    this.node.appendChild(this.listEl);

    this.refresh();
  }

  async refresh(): Promise<void> {
    try {
      this.conversations = await listConversations();
      this.renderList();
    } catch (err: any) {
      this.listEl.innerHTML = `<div class="hub-chat-sidebar-error">${err.message}</div>`;
    }
  }

  private renderList(): void {
    this.listEl.innerHTML = '';

    if (this.conversations.length === 0) {
      this.listEl.innerHTML = '<div class="hub-chat-sidebar-empty">No conversations yet</div>';
      return;
    }

    // Group by folder (null = ungrouped)
    const grouped = new Map<string, Conversation[]>();
    for (const conv of this.conversations) {
      const key = conv.folder || '';
      if (!grouped.has(key)) grouped.set(key, []);
      grouped.get(key)!.push(conv);
    }

    // Render ungrouped first, then folders
    const ungrouped = grouped.get('') || [];
    for (const conv of ungrouped) {
      this.listEl.appendChild(this.renderConvItem(conv));
    }

    for (const [folder, convs] of grouped) {
      if (folder === '') continue;
      const folderEl = document.createElement('div');
      folderEl.className = 'hub-chat-sidebar-folder';
      folderEl.innerHTML = `<div class="hub-chat-sidebar-folder-name">${esc(folder)}</div>`;
      for (const conv of convs) {
        folderEl.appendChild(this.renderConvItem(conv));
      }
      this.listEl.appendChild(folderEl);
    }
  }

  private renderConvItem(conv: Conversation): HTMLElement {
    const el = document.createElement('div');
    el.className = 'hub-chat-sidebar-item';
    el.dataset.id = conv.id;

    const modeIcon = conv.mode === 'agent' ? '🤖' : conv.mode === 'tools' ? '🔧' : '💬';
    el.innerHTML = `
      <span class="hub-chat-sidebar-item-icon">${modeIcon}</span>
      <span class="hub-chat-sidebar-item-title">${esc(conv.title)}</span>
    `;

    el.addEventListener('click', () => {
      this.onOpen(conv.id, conv.title);
    });

    // Context menu
    el.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      this.showContextMenu(e, conv);
    });

    return el;
  }

  private showNewChatDialog(): void {
    const dialog = document.createElement('div');
    dialog.className = 'hub-chat-sidebar-dialog';
    dialog.innerHTML = `
      <div class="hub-chat-sidebar-dialog-title">New Chat</div>
      <select id="hc-mode">
        <option value="tools">LLM + Tools</option>
        <option value="llm">LLM only</option>
        <option value="agent">Agent</option>
      </select>
      <input type="text" id="hc-title" placeholder="Title (optional)" />
      <div class="hub-chat-sidebar-dialog-actions">
        <button id="hc-create">Create</button>
        <button id="hc-cancel">Cancel</button>
      </div>
    `;

    this.listEl.prepend(dialog);

    dialog.querySelector('#hc-cancel')!.addEventListener('click', () => dialog.remove());
    dialog.querySelector('#hc-create')!.addEventListener('click', async () => {
      const mode = (dialog.querySelector('#hc-mode') as HTMLSelectElement).value as 'llm' | 'tools' | 'agent';
      const title = (dialog.querySelector('#hc-title') as HTMLInputElement).value.trim();
      dialog.remove();

      try {
        const conv = await createConversation(mode, title || undefined);
        await this.refresh();
        this.onOpen(conv.id, conv.title);
      } catch (err: any) {
        alert(err.message);
      }
    });
  }

  private showContextMenu(e: MouseEvent, conv: Conversation): void {
    // Remove any existing context menu
    document.querySelectorAll('.hub-chat-ctx-menu').forEach(m => m.remove());

    const menu = document.createElement('div');
    menu.className = 'hub-chat-ctx-menu';
    menu.style.left = e.pageX + 'px';
    menu.style.top = e.pageY + 'px';

    const rename = document.createElement('div');
    rename.className = 'hub-chat-ctx-menu-item';
    rename.textContent = 'Rename';
    rename.addEventListener('click', async () => {
      menu.remove();
      const newTitle = prompt('New title:', conv.title);
      if (newTitle && newTitle !== conv.title) {
        await renameConversation(conv.id, newTitle);
        this.refresh();
      }
    });
    menu.appendChild(rename);

    const del = document.createElement('div');
    del.className = 'hub-chat-ctx-menu-item hub-chat-ctx-menu-item--danger';
    del.textContent = 'Delete';
    del.addEventListener('click', async () => {
      menu.remove();
      if (confirm(`Delete "${conv.title}"?`)) {
        await deleteConversation(conv.id);
        this.refresh();
      }
    });
    menu.appendChild(del);

    document.body.appendChild(menu);
    const close = () => { menu.remove(); document.removeEventListener('click', close); };
    setTimeout(() => document.addEventListener('click', close), 0);
  }
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
