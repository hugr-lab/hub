/**
 * ChatSidebar — conversation tree in left sidebar.
 * Uses ConversationTree for hierarchical display with branches.
 * Two chat modes: Quick Chat (LLM direct) and Agent.
 */
import { Widget } from '@lumino/widgets';
import { MainAreaWidget } from '@jupyterlab/apputils';
import {
  listConversations, createConversation, renameConversation, deleteConversation,
  moveConversation, listModels, myAvailableModelsGQL, listAgentInstances, hasLLM,
  Conversation,
} from '../convApiGraphQL.js';
import { ConversationTree } from './ConversationTree.js';

type OpenCallback = (conversationId: string, title: string) => void;

export class ChatSidebarWidget extends Widget {
  private onOpen: OpenCallback;
  private openWidgets: Map<string, MainAreaWidget<any>>;
  private conversations: Conversation[] = [];
  private listEl: HTMLDivElement;
  private tree: ConversationTree;
  private bannerEl: HTMLDivElement | null = null;

  constructor(onOpen: OpenCallback, openWidgets: Map<string, MainAreaWidget<any>>) {
    super();
    this.onOpen = onOpen;
    this.openWidgets = openWidgets;
    this.id = 'hub-chat-sidebar';
    this.title.label = '';
    this.title.caption = 'Chats';
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

    // LLM availability banner
    this.bannerEl = document.createElement('div');
    this.bannerEl.className = 'hub-chat-sidebar-banner';
    this.bannerEl.style.display = 'none';
    this.node.appendChild(this.bannerEl);

    // Conversation list
    this.listEl = document.createElement('div');
    this.listEl.className = 'hub-chat-sidebar-list';
    this.node.appendChild(this.listEl);

    this.tree = new ConversationTree(
      this.listEl,
      (id, title) => this.onOpen(id, title),
      (e, conv) => this.showContextMenu(e, conv),
    );

    this.refresh();
  }

  async refresh(): Promise<void> {
    try {
      // Check LLM availability
      await listModels();
      if (!hasLLM() && this.bannerEl) {
        this.bannerEl.style.display = '';
        this.bannerEl.innerHTML = `
          <div class="hub-chat-sidebar-banner-icon">ℹ️</div>
          <div class="hub-chat-sidebar-banner-text">
            LLM models not configured. Add an LLM data source to Hugr to enable AI chat.
            Agent mode is still available if agents are configured.
          </div>
        `;
      } else if (this.bannerEl) {
        this.bannerEl.style.display = 'none';
      }

      this.conversations = await listConversations();
      this.tree.render(this.conversations);
    } catch (err: any) {
      const errEl = document.createElement('div');
      errEl.className = 'hub-chat-sidebar-error';
      errEl.textContent = err.message;
      this.listEl.innerHTML = '';
      this.listEl.appendChild(errEl);
    }
  }

  private async showNewChatDialog(): Promise<void> {
    const llmAvailable = hasLLM();

    const dialog = document.createElement('div');
    dialog.className = 'hub-chat-sidebar-dialog';

    const llmDisabled = !llmAvailable ? 'disabled' : '';
    const llmTooltip = !llmAvailable ? 'title="No LLM models configured"' : '';

    dialog.innerHTML = `
      <div class="hub-chat-sidebar-dialog-title">New Chat</div>
      <select id="hc-mode">
        <option value="agent">Agent</option>
        <option value="llm" ${llmDisabled} ${llmTooltip}>Quick Chat</option>
      </select>
      <div id="hc-model-row" style="display:none">
        <select id="hc-model"><option value="">Loading models...</option></select>
      </div>
      <div id="hc-agent-row" style="display:none">
        <select id="hc-agent"><option value="">Loading agents...</option></select>
      </div>
      <input type="text" id="hc-title" placeholder="Title (optional)" />
      <div class="hub-chat-sidebar-dialog-actions">
        <button id="hc-create">Create</button>
        <button id="hc-cancel">Cancel</button>
      </div>
    `;

    this.listEl.prepend(dialog);

    const modeSelect = dialog.querySelector('#hc-mode') as HTMLSelectElement;
    const modelRow = dialog.querySelector('#hc-model-row') as HTMLDivElement;
    const modelSelect = dialog.querySelector('#hc-model') as HTMLSelectElement;
    const agentRow = dialog.querySelector('#hc-agent-row') as HTMLDivElement;
    const agentSelect = dialog.querySelector('#hc-agent') as HTMLSelectElement;
    let modelsLoaded = false;
    let agentsLoaded = false;

    const updateModeUI = async () => {
      modelRow.style.display = 'none';
      agentRow.style.display = 'none';

      if (modeSelect.value === 'llm') {
        modelRow.style.display = '';
        if (!modelsLoaded) {
          modelsLoaded = true;
          const models = await myAvailableModelsGQL();
          modelSelect.innerHTML = '';
          if (models.length === 0) {
            modelSelect.innerHTML = '<option value="">No models available</option>';
          } else {
            for (const m of models) {
              const opt = document.createElement('option');
              opt.value = m.name;
              opt.textContent = `${m.name} (${m.provider})`;
              modelSelect.appendChild(opt);
            }
          }
        }
      } else if (modeSelect.value === 'agent') {
        agentRow.style.display = '';
        if (!agentsLoaded) {
          agentsLoaded = true;
          const agents = await listAgentInstances();
          agentSelect.innerHTML = '';
          if (agents.length === 0) {
            agentSelect.innerHTML = '<option value="">No agents available</option>';
          } else {
            for (const a of agents) {
              const opt = document.createElement('option');
              opt.value = a.id;
              opt.textContent = `${a.display_name || a.agent_type_id}`;
              agentSelect.appendChild(opt);
            }
          }
        }
      }
    };
    modeSelect.addEventListener('change', updateModeUI);
    updateModeUI();

    dialog.querySelector('#hc-cancel')!.addEventListener('click', () => dialog.remove());
    dialog.querySelector('#hc-create')!.addEventListener('click', async () => {
      const mode = modeSelect.value as 'llm' | 'agent';
      const title = (dialog.querySelector('#hc-title') as HTMLInputElement).value.trim();
      const model = mode === 'llm' ? modelSelect.value : undefined;
      const agentId = mode === 'agent' ? agentSelect.value : undefined;
      dialog.remove();

      try {
        const result = await createConversation({
          mode,
          title: title || undefined,
          agent_id: agentId || undefined,
          model: model || undefined,
        });
        await this.refresh();
        const convId = result?.id;
        const convTitle = result?.title || title || 'New Chat';
        if (convId) {
          this.onOpen(convId, convTitle);
        }
      } catch (err: any) {
        alert(err.message);
      }
    });
  }

  private showContextMenu(e: MouseEvent, conv: Conversation): void {
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
        try {
          await renameConversation(conv.id, newTitle);
          const openTab = this.openWidgets.get(conv.id);
          if (openTab && !openTab.isDisposed) {
            openTab.title.label = newTitle;
          }
          this.refresh();
        } catch (err: any) { console.error('rename failed:', err); }
      }
    });
    menu.appendChild(rename);

    const move = document.createElement('div');
    move.className = 'hub-chat-ctx-menu-item';
    move.textContent = 'Move to folder';
    move.addEventListener('click', async () => {
      menu.remove();
      const folder = prompt('Folder name (empty to remove from folder):', conv.folder || '');
      if (folder !== null) {
        try {
          await moveConversation(conv.id, folder || null);
          this.refresh();
        } catch (err: any) { console.error('move failed:', err); }
      }
    });
    menu.appendChild(move);

    const del = document.createElement('div');
    del.className = 'hub-chat-ctx-menu-item hub-chat-ctx-menu-item--danger';
    del.textContent = 'Delete';
    del.addEventListener('click', async () => {
      menu.remove();
      if (confirm(`Delete "${conv.title}"?`)) {
        try {
          const openTab = this.openWidgets.get(conv.id);
          if (openTab && !openTab.isDisposed) {
            openTab.dispose();
          }
          await deleteConversation(conv.id);
          this.refresh();
        } catch (err: any) { console.error('delete failed:', err); }
      }
    });
    menu.appendChild(del);

    document.body.appendChild(menu);
    const close = () => { menu.remove(); document.removeEventListener('click', close); };
    setTimeout(() => document.addEventListener('click', close), 0);
  }
}
