/**
 * Hub Admin Panel — main widget with tabbed sections.
 * Uses plain DOM (Lumino Widget) consistent with hugr-graphql-ide patterns.
 */
import { Widget } from '@lumino/widgets';

import {
  fetchAgentInstances,
  fetchLLMProviders,
  fetchLLMBudgets,
  fetchLLMUsage,
  fetchAgentSessions,
  updateLLMProvider,
  stopAgent,
  clearAgentMemory,
  insertLLMProvider,
  insertLLMBudget,
  deleteLLMBudget,
} from '../api.js';
import type { AgentInstance, LLMProvider, LLMBudget, LLMUsage } from '../api.js';

type Tab = 'dashboard' | 'providers' | 'budgets' | 'agents';

export class AdminPanelWidget extends Widget {
  private activeTab: Tab = 'dashboard';
  private content: HTMLDivElement;

  constructor() {
    super();
    this.id = 'hub-admin-panel';
    this.title.label = 'Hub Admin';
    this.title.closable = true;
    this.addClass('hub-admin-panel');

    this.content = document.createElement('div');
    this.node.appendChild(this.content);

    this.render();
  }

  private render(): void {
    this.content.innerHTML = '';

    // Tabs
    const tabs = document.createElement('div');
    tabs.className = 'hub-admin-tabs';
    const tabItems: { key: Tab; label: string }[] = [
      { key: 'dashboard', label: 'Dashboard' },
      { key: 'providers', label: 'LLM Providers' },
      { key: 'budgets', label: 'Budgets' },
      { key: 'agents', label: 'Agents' },
    ];
    for (const item of tabItems) {
      const tab = document.createElement('div');
      tab.className = 'hub-admin-tab';
      if (item.key === this.activeTab) {
        tab.classList.add('hub-admin-tab--active');
      }
      tab.textContent = item.label;
      tab.addEventListener('click', () => {
        this.activeTab = item.key;
        this.render();
      });
      tabs.appendChild(tab);
    }
    this.content.appendChild(tabs);

    // Tab content
    const body = document.createElement('div');
    this.content.appendChild(body);

    switch (this.activeTab) {
      case 'dashboard':
        this.renderDashboard(body);
        break;
      case 'providers':
        this.renderProviders(body);
        break;
      case 'budgets':
        this.renderBudgets(body);
        break;
      case 'agents':
        this.renderAgents(body);
        break;
    }
  }

  // ── Dashboard ──────────────────────────────────────────────────────

  private async renderDashboard(container: HTMLElement): Promise<void> {
    container.innerHTML = '<div class="hub-admin-loading">Loading...</div>';

    try {
      const [instances, sessions, usage, providers] = await Promise.all([
        fetchAgentInstances(),
        fetchAgentSessions(),
        fetchLLMUsage(1000),
        fetchLLMProviders(),
      ]);

      container.innerHTML = '';

      // Stats
      const stats = document.createElement('div');
      stats.className = 'hub-admin-stats';

      const running = instances.filter(i => i.status === 'running').length;
      const totalSessions = sessions.length;
      const totalTokens = usage.reduce((s, u) => s + u.input_tokens + u.output_tokens, 0);
      const activeProviders = providers.filter(p => p.enabled).length;

      stats.innerHTML = `
        <div class="hub-admin-stat">
          <div class="hub-admin-stat-value">${running}</div>
          <div class="hub-admin-stat-label">Running Agents</div>
        </div>
        <div class="hub-admin-stat">
          <div class="hub-admin-stat-value">${totalSessions}</div>
          <div class="hub-admin-stat-label">Sessions</div>
        </div>
        <div class="hub-admin-stat">
          <div class="hub-admin-stat-value">${formatTokens(totalTokens)}</div>
          <div class="hub-admin-stat-label">Total Tokens</div>
        </div>
        <div class="hub-admin-stat">
          <div class="hub-admin-stat-value">${activeProviders}</div>
          <div class="hub-admin-stat-label">Active Providers</div>
        </div>
      `;
      container.appendChild(stats);

      // Recent sessions table
      container.appendChild(createHeading('Recent Sessions'));
      if (sessions.length === 0) {
        container.appendChild(createEmpty('No sessions yet'));
      } else {
        const table = createTable(
          ['User', 'Started', 'Messages'],
          sessions.slice(0, 10).map(s => [
            s.user_id,
            formatDate(s.started_at),
            String(s.message_count ?? 0),
          ]),
        );
        container.appendChild(table);
      }

      // Recent usage
      container.appendChild(createHeading('Recent LLM Usage'));
      if (usage.length === 0) {
        container.appendChild(createEmpty('No usage data'));
      } else {
        const table = createTable(
          ['User', 'Provider', 'In', 'Out', 'Time'],
          usage.slice(0, 10).map(u => [
            u.user_id,
            u.provider_id,
            String(u.input_tokens),
            String(u.output_tokens),
            formatDate(u.created_at),
          ]),
        );
        container.appendChild(table);
      }
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  // ── Providers ──────────────────────────────────────────────────────

  private async renderProviders(container: HTMLElement): Promise<void> {
    container.innerHTML = '<div class="hub-admin-loading">Loading...</div>';

    try {
      const providers = await fetchLLMProviders();
      container.innerHTML = '';

      // Add button
      const addBtn = document.createElement('button');
      addBtn.className = 'hub-admin-btn hub-admin-btn--primary';
      addBtn.textContent = 'Add Provider';
      addBtn.addEventListener('click', () => this.showAddProviderForm(container));
      container.appendChild(addBtn);

      if (providers.length === 0) {
        container.appendChild(createEmpty('No providers configured'));
        return;
      }

      const table = document.createElement('table');
      table.className = 'hub-admin-table';
      table.innerHTML = `
        <thead><tr>
          <th>Name</th><th>Type</th><th>Model</th><th>Status</th><th>Actions</th>
        </tr></thead>
      `;
      const tbody = document.createElement('tbody');

      for (const p of providers) {
        const tr = document.createElement('tr');
        const statusClass = p.enabled ? 'hub-admin-badge--running' : 'hub-admin-badge--stopped';
        const statusText = p.enabled ? 'Active' : 'Disabled';

        tr.innerHTML = `
          <td>${esc(p.name)}</td>
          <td>${esc(p.provider_type)}</td>
          <td>${esc(p.model)}</td>
          <td><span class="hub-admin-badge ${statusClass}">${statusText}</span></td>
          <td></td>
        `;

        const actionsCell = tr.querySelector('td:last-child')!;
        const toggleBtn = document.createElement('button');
        toggleBtn.className = 'hub-admin-btn';
        toggleBtn.textContent = p.enabled ? 'Disable' : 'Enable';
        toggleBtn.addEventListener('click', async () => {
          await updateLLMProvider(p.id, { enabled: !p.enabled });
          this.render();
        });
        actionsCell.appendChild(toggleBtn);

        tbody.appendChild(tr);
      }

      table.appendChild(tbody);
      container.appendChild(table);
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddProviderForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-section';
    form.innerHTML = `
      <h3>Add LLM Provider</h3>
      <div class="hub-admin-form-group">
        <label>Name</label>
        <input type="text" id="prov-name" placeholder="claude-sonnet" />
      </div>
      <div class="hub-admin-form-group">
        <label>Type</label>
        <select id="prov-type">
          <option value="anthropic">Anthropic</option>
          <option value="openai">OpenAI</option>
          <option value="gemini">Gemini</option>
          <option value="openai_compatible">OpenAI Compatible</option>
        </select>
      </div>
      <div class="hub-admin-form-group">
        <label>Base URL (optional for standard providers)</label>
        <input type="text" id="prov-url" placeholder="https://api.anthropic.com" />
      </div>
      <div class="hub-admin-form-group">
        <label>Model</label>
        <input type="text" id="prov-model" placeholder="claude-sonnet-4-20250514" />
      </div>
    `;

    const btnRow = document.createElement('div');
    btnRow.style.display = 'flex';
    btnRow.style.gap = '8px';
    btnRow.style.marginTop = '8px';

    const saveBtn = document.createElement('button');
    saveBtn.className = 'hub-admin-btn hub-admin-btn--primary';
    saveBtn.textContent = 'Save';
    saveBtn.addEventListener('click', async () => {
      const name = (form.querySelector('#prov-name') as HTMLInputElement).value;
      const type = (form.querySelector('#prov-type') as HTMLSelectElement).value;
      const url = (form.querySelector('#prov-url') as HTMLInputElement).value;
      const model = (form.querySelector('#prov-model') as HTMLInputElement).value;

      if (!name || !model) return;

      await insertLLMProvider({
        name,
        provider_type: type,
        base_url: url,
        model,
        enabled: true,
        is_default: false,
      });
      this.render();
    });

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'hub-admin-btn';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', () => this.render());

    btnRow.appendChild(saveBtn);
    btnRow.appendChild(cancelBtn);
    form.appendChild(btnRow);

    container.prepend(form);
  }

  // ── Budgets ────────────────────────────────────────────────────────

  private async renderBudgets(container: HTMLElement): Promise<void> {
    container.innerHTML = '<div class="hub-admin-loading">Loading...</div>';

    try {
      const budgets = await fetchLLMBudgets();
      container.innerHTML = '';

      const addBtn = document.createElement('button');
      addBtn.className = 'hub-admin-btn hub-admin-btn--primary';
      addBtn.textContent = 'Add Budget';
      addBtn.addEventListener('click', () => this.showAddBudgetForm(container));
      container.appendChild(addBtn);

      if (budgets.length === 0) {
        container.appendChild(createEmpty('No budgets configured'));
        return;
      }

      const table = document.createElement('table');
      table.className = 'hub-admin-table';
      table.innerHTML = `
        <thead><tr>
          <th>Scope</th><th>ID</th><th>Period</th><th>Max Tokens</th><th>Max Reqs</th><th>Actions</th>
        </tr></thead>
      `;
      const tbody = document.createElement('tbody');

      for (const b of budgets) {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td>${esc(b.scope)}</td>
          <td>${esc(b.scope_id)}</td>
          <td>${esc(b.period)}</td>
          <td>${formatTokens(b.max_tokens)}</td>
          <td>${b.max_requests}</td>
          <td></td>
        `;

        const actionsCell = tr.querySelector('td:last-child')!;
        const delBtn = document.createElement('button');
        delBtn.className = 'hub-admin-btn hub-admin-btn--danger';
        delBtn.textContent = 'Delete';
        delBtn.addEventListener('click', async () => {
          await deleteLLMBudget(b.id);
          this.render();
        });
        actionsCell.appendChild(delBtn);

        tbody.appendChild(tr);
      }

      table.appendChild(tbody);
      container.appendChild(table);
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddBudgetForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-section';
    form.innerHTML = `
      <h3>Add Budget</h3>
      <div class="hub-admin-form-group">
        <label>Scope</label>
        <select id="bud-scope">
          <option value="global">Global</option>
          <option value="role">Role</option>
          <option value="user">User</option>
        </select>
      </div>
      <div class="hub-admin-form-group">
        <label>Scope ID (role name or user ID, empty for global)</label>
        <input type="text" id="bud-scope-id" />
      </div>
      <div class="hub-admin-form-group">
        <label>Period</label>
        <select id="bud-period">
          <option value="daily">Daily</option>
          <option value="weekly">Weekly</option>
          <option value="monthly">Monthly</option>
        </select>
      </div>
      <div class="hub-admin-form-group">
        <label>Max Tokens</label>
        <input type="number" id="bud-tokens" value="1000000" />
      </div>
      <div class="hub-admin-form-group">
        <label>Max Requests</label>
        <input type="number" id="bud-requests" value="1000" />
      </div>
    `;

    const btnRow = document.createElement('div');
    btnRow.style.display = 'flex';
    btnRow.style.gap = '8px';
    btnRow.style.marginTop = '8px';

    const saveBtn = document.createElement('button');
    saveBtn.className = 'hub-admin-btn hub-admin-btn--primary';
    saveBtn.textContent = 'Save';
    saveBtn.addEventListener('click', async () => {
      const scope = (form.querySelector('#bud-scope') as HTMLSelectElement).value;
      const scopeId = (form.querySelector('#bud-scope-id') as HTMLInputElement).value;
      const period = (form.querySelector('#bud-period') as HTMLSelectElement).value;
      const maxTokens = parseInt((form.querySelector('#bud-tokens') as HTMLInputElement).value, 10);
      const maxRequests = parseInt((form.querySelector('#bud-requests') as HTMLInputElement).value, 10);

      await insertLLMBudget({
        scope,
        scope_id: scopeId || '*',
        period,
        max_tokens: maxTokens,
        max_requests: maxRequests,
      });
      this.render();
    });

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'hub-admin-btn';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', () => this.render());

    btnRow.appendChild(saveBtn);
    btnRow.appendChild(cancelBtn);
    form.appendChild(btnRow);

    container.prepend(form);
  }

  // ── Agents ─────────────────────────────────────────────────────────

  private async renderAgents(container: HTMLElement): Promise<void> {
    container.innerHTML = '<div class="hub-admin-loading">Loading...</div>';

    try {
      const instances = await fetchAgentInstances();
      container.innerHTML = '';

      if (instances.length === 0) {
        container.appendChild(createEmpty('No agent instances'));
        return;
      }

      const table = document.createElement('table');
      table.className = 'hub-admin-table';
      table.innerHTML = `
        <thead><tr>
          <th>User</th><th>Type</th><th>Status</th><th>Started</th><th>Actions</th>
        </tr></thead>
      `;
      const tbody = document.createElement('tbody');

      for (const inst of instances) {
        const tr = document.createElement('tr');
        const statusClass =
          inst.status === 'running'
            ? 'hub-admin-badge--running'
            : inst.status === 'error'
              ? 'hub-admin-badge--error'
              : 'hub-admin-badge--stopped';

        tr.innerHTML = `
          <td>${esc(inst.user_id)}</td>
          <td>${esc(inst.agent_type_id)}</td>
          <td><span class="hub-admin-badge ${statusClass}">${esc(inst.status)}</span></td>
          <td>${formatDate(inst.started_at)}</td>
          <td></td>
        `;

        const actionsCell = tr.querySelector('td:last-child')!;

        if (inst.status === 'running') {
          const stopBtn = document.createElement('button');
          stopBtn.className = 'hub-admin-btn hub-admin-btn--danger';
          stopBtn.textContent = 'Stop';
          stopBtn.addEventListener('click', async () => {
            await stopAgent(inst.id);
            this.render();
          });
          actionsCell.appendChild(stopBtn);
        }

        const clearBtn = document.createElement('button');
        clearBtn.className = 'hub-admin-btn';
        clearBtn.textContent = 'Clear Memory';
        clearBtn.style.marginLeft = '4px';
        clearBtn.addEventListener('click', async () => {
          await clearAgentMemory(inst.user_id);
          this.render();
        });
        actionsCell.appendChild(clearBtn);

        tbody.appendChild(tr);
      }

      table.appendChild(tbody);
      container.appendChild(table);
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }
}

// ── Helpers ────────────────────────────────────────────────────────────

function createHeading(text: string): HTMLElement {
  const h = document.createElement('h2');
  h.textContent = text;
  return h;
}

function createEmpty(text: string): HTMLElement {
  const div = document.createElement('div');
  div.className = 'hub-admin-empty';
  div.textContent = text;
  return div;
}

function createTable(headers: string[], rows: string[][]): HTMLTableElement {
  const table = document.createElement('table');
  table.className = 'hub-admin-table';

  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const h of headers) {
    const th = document.createElement('th');
    th.textContent = h;
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (const row of rows) {
    const tr = document.createElement('tr');
    for (const cell of row) {
      const td = document.createElement('td');
      td.textContent = cell;
      tr.appendChild(td);
    }
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);

  return table;
}

function formatDate(iso: string): string {
  if (!iso) return '-';
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function esc(s: string): string {
  const div = document.createElement('div');
  div.textContent = s ?? '';
  return div.innerHTML;
}
