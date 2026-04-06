/**
 * Hub Admin Panel — sidebar widget with collapsible sections.
 * Uses JupyterLab sidebar pattern (vertical collapsible sections like Running panel).
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
import type { AgentInstance, AgentSession, LLMProvider, LLMBudget, LLMUsage } from '../api.js';

type Section = 'dashboard' | 'providers' | 'budgets' | 'agents';

interface SectionDef {
  key: Section;
  label: string;
}

const SECTIONS: SectionDef[] = [
  { key: 'dashboard', label: 'DASHBOARD' },
  { key: 'providers', label: 'LLM PROVIDERS' },
  { key: 'budgets', label: 'BUDGETS' },
  { key: 'agents', label: 'AGENTS' },
];

export class AdminPanelWidget extends Widget {
  private expanded: Record<Section, boolean> = {
    dashboard: true,
    providers: false,
    budgets: false,
    agents: false,
  };
  private sectionBodies: Record<Section, HTMLDivElement> = {} as any;
  private sectionHeaders: Record<Section, HTMLDivElement> = {} as any;

  constructor() {
    super();
    this.id = 'hub-admin-panel';
    this.title.closable = true;
    this.addClass('hub-admin-panel');

    this.buildLayout();
  }

  private buildLayout(): void {
    for (const section of SECTIONS) {
      // Header
      const header = document.createElement('div');
      header.className = 'hub-admin-section-header';
      header.innerHTML = `<span class="hub-admin-section-caret">&#9654;</span> ${section.label}`;
      header.addEventListener('click', () => this.toggleSection(section.key));
      this.node.appendChild(header);
      this.sectionHeaders[section.key] = header;

      // Body
      const body = document.createElement('div');
      body.className = 'hub-admin-section-body';
      body.style.display = this.expanded[section.key] ? '' : 'none';
      this.node.appendChild(body);
      this.sectionBodies[section.key] = body;

      if (this.expanded[section.key]) {
        header.classList.add('hub-admin-section-header--expanded');
      }
    }

    // Load initial expanded sections
    this.loadSection('dashboard');
  }

  private toggleSection(key: Section): void {
    this.expanded[key] = !this.expanded[key];
    const body = this.sectionBodies[key];
    const header = this.sectionHeaders[key];

    if (this.expanded[key]) {
      body.style.display = '';
      header.classList.add('hub-admin-section-header--expanded');
      this.loadSection(key);
    } else {
      body.style.display = 'none';
      header.classList.remove('hub-admin-section-header--expanded');
    }
  }

  private loadSection(key: Section): void {
    const body = this.sectionBodies[key];
    switch (key) {
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

      const running = instances.filter(i => i.status === 'running').length;
      const totalSessions = sessions.length;
      const totalTokens = usage.reduce((s, u) => s + u.tokens_in + u.tokens_out, 0);
      const activeProviders = providers.filter(p => p.enabled).length;

      const stats = document.createElement('div');
      stats.className = 'hub-admin-stats';
      stats.innerHTML = `
        <div class="hub-admin-stat">
          <span class="hub-admin-stat-value">${running}</span>
          <span class="hub-admin-stat-label">Running Agents</span>
        </div>
        <div class="hub-admin-stat">
          <span class="hub-admin-stat-value">${totalSessions}</span>
          <span class="hub-admin-stat-label">Sessions</span>
        </div>
        <div class="hub-admin-stat">
          <span class="hub-admin-stat-value">${formatTokens(totalTokens)}</span>
          <span class="hub-admin-stat-label">Total Tokens</span>
        </div>
        <div class="hub-admin-stat">
          <span class="hub-admin-stat-value">${activeProviders}</span>
          <span class="hub-admin-stat-label">Active Providers</span>
        </div>
      `;
      container.appendChild(stats);

      // Recent sessions
      if (sessions.length > 0) {
        container.appendChild(createSubheading('Recent Sessions'));
        container.appendChild(
          createTable(
            ['User', 'Started', 'Ended'],
            sessions.slice(0, 10).map(s => [
              s.user_id,
              formatDate(s.started_at),
              s.ended_at ? formatDate(s.ended_at) : 'active',
            ]),
          ),
        );
      }

      // Recent usage
      if (usage.length > 0) {
        container.appendChild(createSubheading('Recent LLM Usage'));
        container.appendChild(
          createTable(
            ['User', 'Provider', 'In', 'Out', 'Time'],
            usage.slice(0, 10).map(u => [
              u.user_id,
              u.provider_id,
              String(u.tokens_in),
              String(u.tokens_out),
              formatDate(u.created_at),
            ]),
          ),
        );
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

      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = document.createElement('button');
      addBtn.className = 'hub-admin-btn hub-admin-btn--primary';
      addBtn.textContent = '+ Add Provider';
      addBtn.addEventListener('click', () => this.showAddProviderForm(container));
      toolbar.appendChild(addBtn);
      container.appendChild(toolbar);

      if (providers.length === 0) {
        container.appendChild(createEmpty('No providers configured'));
        return;
      }

      for (const p of providers) {
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';

        const statusDot = p.enabled ? 'hub-admin-dot--active' : 'hub-admin-dot--inactive';
        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-dot ${statusDot}"></span>
            <span class="hub-admin-list-item-title">${esc(p.id)}</span>
            <span class="hub-admin-list-item-meta">${esc(p.provider)} / ${esc(p.model)}</span>
          </div>
        `;

        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';

        const toggleBtn = document.createElement('button');
        toggleBtn.className = 'hub-admin-btn-sm';
        toggleBtn.textContent = p.enabled ? 'Disable' : 'Enable';
        toggleBtn.addEventListener('click', async () => {
          await updateLLMProvider(p.id, { enabled: !p.enabled });
          this.loadSection('providers');
        });
        actions.appendChild(toggleBtn);

        row.appendChild(actions);
        container.appendChild(row);
      }
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddProviderForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-form';
    form.innerHTML = `
      <div class="hub-admin-form-group">
        <label>ID</label>
        <input type="text" id="prov-id" placeholder="claude-main" />
      </div>
      <div class="hub-admin-form-group">
        <label>Provider</label>
        <select id="prov-type">
          <option value="anthropic">Anthropic</option>
          <option value="openai">OpenAI</option>
          <option value="gemini">Gemini</option>
          <option value="openai_compatible">OpenAI Compatible</option>
        </select>
      </div>
      <div class="hub-admin-form-group">
        <label>Base URL (optional)</label>
        <input type="text" id="prov-url" placeholder="https://api.anthropic.com" />
      </div>
      <div class="hub-admin-form-group">
        <label>Model</label>
        <input type="text" id="prov-model" placeholder="claude-sonnet-4-20250514" />
      </div>
    `;

    const btnRow = document.createElement('div');
    btnRow.className = 'hub-admin-form-actions';

    const saveBtn = document.createElement('button');
    saveBtn.className = 'hub-admin-btn hub-admin-btn--primary';
    saveBtn.textContent = 'Save';
    saveBtn.addEventListener('click', async () => {
      const id = (form.querySelector('#prov-id') as HTMLInputElement).value;
      const provider = (form.querySelector('#prov-type') as HTMLSelectElement).value;
      const url = (form.querySelector('#prov-url') as HTMLInputElement).value;
      const model = (form.querySelector('#prov-model') as HTMLInputElement).value;
      if (!id || !model) return;
      await insertLLMProvider({
        id, provider, base_url: url, model, max_tokens_per_request: 4096, enabled: true,
      });
      this.loadSection('providers');
    });

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'hub-admin-btn';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', () => this.loadSection('providers'));

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

      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = document.createElement('button');
      addBtn.className = 'hub-admin-btn hub-admin-btn--primary';
      addBtn.textContent = '+ Add Budget';
      addBtn.addEventListener('click', () => this.showAddBudgetForm(container));
      toolbar.appendChild(addBtn);
      container.appendChild(toolbar);

      if (budgets.length === 0) {
        container.appendChild(createEmpty('No budgets configured'));
        return;
      }

      for (const b of budgets) {
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';
        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-list-item-title">${esc(b.scope)}${b.provider_id ? ' (' + esc(b.provider_id) + ')' : ''}</span>
            <span class="hub-admin-list-item-meta">${esc(b.period)} — in:${formatTokens(b.max_tokens_in ?? 0)} out:${formatTokens(b.max_tokens_out ?? 0)}, ${b.max_requests ?? 0} reqs</span>
          </div>
        `;

        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';
        const delBtn = document.createElement('button');
        delBtn.className = 'hub-admin-btn-sm hub-admin-btn--danger';
        delBtn.textContent = 'Delete';
        delBtn.addEventListener('click', async () => {
          await deleteLLMBudget(b.id);
          this.loadSection('budgets');
        });
        actions.appendChild(delBtn);

        row.appendChild(actions);
        container.appendChild(row);
      }
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddBudgetForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-form';
    form.innerHTML = `
      <div class="hub-admin-form-group">
        <label>Scope (e.g. global, user:alice, role:analyst)</label>
        <input type="text" id="bud-scope" value="global" />
      </div>
      <div class="hub-admin-form-group">
        <label>Provider ID (empty = all providers)</label>
        <input type="text" id="bud-provider" />
      </div>
      <div class="hub-admin-form-group">
        <label>Period</label>
        <select id="bud-period">
          <option value="hour">Hour</option>
          <option value="day">Day</option>
          <option value="month">Month</option>
        </select>
      </div>
      <div class="hub-admin-form-group">
        <label>Max Input Tokens</label>
        <input type="number" id="bud-tokens-in" value="1000000" />
      </div>
      <div class="hub-admin-form-group">
        <label>Max Output Tokens</label>
        <input type="number" id="bud-tokens-out" value="500000" />
      </div>
      <div class="hub-admin-form-group">
        <label>Max Requests</label>
        <input type="number" id="bud-requests" value="1000" />
      </div>
    `;

    const btnRow = document.createElement('div');
    btnRow.className = 'hub-admin-form-actions';

    const saveBtn = document.createElement('button');
    saveBtn.className = 'hub-admin-btn hub-admin-btn--primary';
    saveBtn.textContent = 'Save';
    saveBtn.addEventListener('click', async () => {
      const scope = (form.querySelector('#bud-scope') as HTMLInputElement).value;
      const providerId = (form.querySelector('#bud-provider') as HTMLInputElement).value;
      const period = (form.querySelector('#bud-period') as HTMLSelectElement).value;
      const maxTokensIn = parseInt((form.querySelector('#bud-tokens-in') as HTMLInputElement).value, 10);
      const maxTokensOut = parseInt((form.querySelector('#bud-tokens-out') as HTMLInputElement).value, 10);
      const maxRequests = parseInt((form.querySelector('#bud-requests') as HTMLInputElement).value, 10);
      await insertLLMBudget({
        scope, provider_id: providerId || null as any, period, max_tokens_in: maxTokensIn, max_tokens_out: maxTokensOut, max_requests: maxRequests,
      });
      this.loadSection('budgets');
    });

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'hub-admin-btn';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', () => this.loadSection('budgets'));

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

      for (const inst of instances) {
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';

        const statusDot =
          inst.status === 'running' ? 'hub-admin-dot--active'
            : inst.status === 'error' ? 'hub-admin-dot--error'
              : 'hub-admin-dot--inactive';

        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-dot ${statusDot}"></span>
            <span class="hub-admin-list-item-title">${esc(inst.user_id)}</span>
            <span class="hub-admin-list-item-meta">${esc(inst.agent_type_id)} — ${formatDate(inst.started_at)}</span>
          </div>
        `;

        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';

        if (inst.status === 'running') {
          const stopBtn = document.createElement('button');
          stopBtn.className = 'hub-admin-btn-sm hub-admin-btn--danger';
          stopBtn.textContent = 'Stop';
          stopBtn.addEventListener('click', async () => {
            await stopAgent(inst.id);
            this.loadSection('agents');
          });
          actions.appendChild(stopBtn);
        }

        const clearBtn = document.createElement('button');
        clearBtn.className = 'hub-admin-btn-sm';
        clearBtn.textContent = 'Clear Memory';
        clearBtn.addEventListener('click', async () => {
          await clearAgentMemory(inst.user_id);
          this.loadSection('agents');
        });
        actions.appendChild(clearBtn);

        row.appendChild(actions);
        container.appendChild(row);
      }
    } catch (err: any) {
      container.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }
}

// ── Helpers ────────────────────────────────────────────────────────────

function createSubheading(text: string): HTMLElement {
  const h = document.createElement('div');
  h.className = 'hub-admin-subheading';
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
