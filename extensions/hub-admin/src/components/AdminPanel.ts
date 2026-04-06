/**
 * Hub Admin Panel — sidebar widget with collapsible sections.
 */
import { Widget } from '@lumino/widgets';

import {
  fetchDataSources,
  fetchDataSourceStatus,
  insertDataSource,
  updateDataSource,
  deleteDataSource,
  loadDataSource,
  unloadDataSource,
  fetchCatalogSources,
  fetchCatalogs,
  insertCatalogSource,
  updateCatalogSource,
  deleteCatalogSource,
  linkCatalog,
  unlinkCatalog,
  fetchModelSources,
  fetchAgentInstances,
  fetchAgentSessions,
  fetchLLMBudgets,
  fetchLLMUsage,
  insertLLMBudget,
  deleteLLMBudget,
  stopAgent,
  clearAgentMemory,
} from '../api.js';
import type {
  DataSource,
  CatalogSource,
  CatalogLink,
  ModelSource,
  LLMBudget,
} from '../api.js';

type Section = 'dashboard' | 'datasources' | 'catalogs' | 'budgets' | 'agents';

const SECTIONS: { key: Section; label: string }[] = [
  { key: 'dashboard', label: 'DASHBOARD' },
  { key: 'datasources', label: 'DATA SOURCES' },
  { key: 'catalogs', label: 'CATALOGS' },
  { key: 'budgets', label: 'BUDGETS' },
  { key: 'agents', label: 'AGENTS' },
];

export class AdminPanelWidget extends Widget {
  private expanded: Record<Section, boolean> = {
    dashboard: true,
    datasources: false,
    catalogs: false,
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
      const header = document.createElement('div');
      header.className = 'hub-admin-section-header';
      header.innerHTML = `<span class="hub-admin-section-caret">&#9654;</span> ${section.label}`;
      header.addEventListener('click', () => this.toggleSection(section.key));
      this.node.appendChild(header);
      this.sectionHeaders[section.key] = header;

      const body = document.createElement('div');
      body.className = 'hub-admin-section-body';
      body.style.display = this.expanded[section.key] ? '' : 'none';
      this.node.appendChild(body);
      this.sectionBodies[section.key] = body;

      if (this.expanded[section.key]) {
        header.classList.add('hub-admin-section-header--expanded');
      }
    }
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
      case 'dashboard': this.renderDashboard(body); break;
      case 'datasources': this.renderDataSources(body); break;
      case 'catalogs': this.renderCatalogs(body); break;
      case 'budgets': this.renderBudgets(body); break;
      case 'agents': this.renderAgents(body); break;
    }
  }

  // ── Dashboard ──────────────────────────────────────────

  private async renderDashboard(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const [instances, sessions, usage, models] = await Promise.all([
        fetchAgentInstances(), fetchAgentSessions(), fetchLLMUsage(1000), fetchModelSources(),
      ]);
      el.innerHTML = '';
      const running = instances.filter(i => i.status === 'running').length;
      const totalTokens = usage.reduce((s, u) => s + u.tokens_in + u.tokens_out, 0);

      const stats = document.createElement('div');
      stats.className = 'hub-admin-stats';
      stats.innerHTML = `
        <div class="hub-admin-stat"><span class="hub-admin-stat-value">${running}</span><span class="hub-admin-stat-label">Running Agents</span></div>
        <div class="hub-admin-stat"><span class="hub-admin-stat-value">${sessions.length}</span><span class="hub-admin-stat-label">Sessions</span></div>
        <div class="hub-admin-stat"><span class="hub-admin-stat-value">${fmtTokens(totalTokens)}</span><span class="hub-admin-stat-label">Total Tokens</span></div>
        <div class="hub-admin-stat"><span class="hub-admin-stat-value">${models.length}</span><span class="hub-admin-stat-label">Models</span></div>
      `;
      el.appendChild(stats);

      if (usage.length > 0) {
        el.appendChild(subheading('Recent LLM Usage'));
        el.appendChild(table(['User', 'Model', 'In', 'Out', 'Time'],
          usage.slice(0, 10).map(u => [u.user_id, u.provider_id, String(u.tokens_in), String(u.tokens_out), fmtDate(u.created_at)])));
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  // ── Data Sources ───────────────────────────────────────

  private async renderDataSources(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const [sources, models, catalogLinks] = await Promise.all([
        fetchDataSources(), fetchModelSources(), fetchCatalogs(),
      ]);
      el.innerHTML = '';

      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = btn('+ Add', 'hub-admin-btn hub-admin-btn--primary');
      addBtn.addEventListener('click', () => this.showAddDataSourceForm(el));
      toolbar.appendChild(addBtn);
      el.appendChild(toolbar);

      if (sources.length === 0) {
        el.appendChild(empty('No data sources'));
        return;
      }

      for (const ds of sources) {
        const modelInfo = models.find(m => m.name === ds.name);
        const dsLinks = catalogLinks.filter(c => c.data_source_name === ds.name);
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';

        const statusDot = ds.disabled ? 'hub-admin-dot--inactive' : 'hub-admin-dot--active';
        let meta = `${esc(ds.type)}`;
        if (modelInfo) meta += ` / ${esc(modelInfo.provider)} / ${esc(modelInfo.model)}`;
        if (dsLinks.length > 0) meta += ` / ${dsLinks.length} catalog(s)`;

        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-dot ${statusDot}"></span>
            <span class="hub-admin-list-item-title">${esc(ds.name)}</span>
            <span class="hub-admin-list-item-meta">${meta}</span>
          </div>
        `;

        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';

        const loadBtn = btn('Load', 'hub-admin-btn-sm');
        loadBtn.addEventListener('click', async () => { await loadDataSource(ds.name); this.loadSection('datasources'); });
        actions.appendChild(loadBtn);

        const unloadBtn = btn('Unload', 'hub-admin-btn-sm');
        unloadBtn.addEventListener('click', async () => { await unloadDataSource(ds.name); this.loadSection('datasources'); });
        actions.appendChild(unloadBtn);

        if (!ds.disabled) {
          const disableBtn = btn('Disable', 'hub-admin-btn-sm');
          disableBtn.addEventListener('click', async () => { await updateDataSource(ds.name, { disabled: true } as any); this.loadSection('datasources'); });
          actions.appendChild(disableBtn);
        } else {
          const enableBtn = btn('Enable', 'hub-admin-btn-sm');
          enableBtn.addEventListener('click', async () => { await updateDataSource(ds.name, { disabled: false } as any); this.loadSection('datasources'); });
          actions.appendChild(enableBtn);
        }

        const delBtn = btn('Delete', 'hub-admin-btn-sm hub-admin-btn--danger');
        delBtn.addEventListener('click', async () => { await deleteDataSource(ds.name); this.loadSection('datasources'); });
        actions.appendChild(delBtn);

        row.appendChild(actions);
        el.appendChild(row);
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddDataSourceForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-form';
    form.innerHTML = `
      <div class="hub-admin-form-group"><label>Name</label><input type="text" id="ds-name" placeholder="my-postgres" /></div>
      <div class="hub-admin-form-group"><label>Type</label>
        <select id="ds-type">
          <option value="postgres">postgres</option>
          <option value="duckdb">duckdb</option>
          <option value="llm-openai">llm-openai</option>
          <option value="llm-anthropic">llm-anthropic</option>
          <option value="llm-gemini">llm-gemini</option>
          <option value="redis">redis</option>
          <option value="http">http</option>
          <option value="embedding">embedding</option>
          <option value="extension">extension</option>
        </select>
      </div>
      <div class="hub-admin-form-group"><label>Path</label><input type="text" id="ds-path" placeholder="postgres://..." /></div>
      <div class="hub-admin-form-group"><label>Description</label><input type="text" id="ds-desc" /></div>
      <div class="hub-admin-form-group"><label><input type="checkbox" id="ds-readonly" /> Read Only</label></div>
      <div class="hub-admin-form-group"><label><input type="checkbox" id="ds-module" /> As Module</label></div>
    `;

    const actions = document.createElement('div');
    actions.className = 'hub-admin-form-actions';
    const saveBtn = btn('Create', 'hub-admin-btn hub-admin-btn--primary');
    saveBtn.addEventListener('click', async () => {
      const name = (form.querySelector('#ds-name') as HTMLInputElement).value;
      const type = (form.querySelector('#ds-type') as HTMLSelectElement).value;
      const path = (form.querySelector('#ds-path') as HTMLInputElement).value;
      const desc = (form.querySelector('#ds-desc') as HTMLInputElement).value;
      const readOnly = (form.querySelector('#ds-readonly') as HTMLInputElement).checked;
      const asModule = (form.querySelector('#ds-module') as HTMLInputElement).checked;
      if (!name || !path) return;
      const prefix = name.replace(/[.-]/g, '_');
      await insertDataSource({ name, type, path, prefix, description: desc, read_only: readOnly, as_module: asModule } as any);
      this.loadSection('datasources');
    });
    const cancelBtn = btn('Cancel', 'hub-admin-btn');
    cancelBtn.addEventListener('click', () => this.loadSection('datasources'));
    actions.appendChild(saveBtn);
    actions.appendChild(cancelBtn);
    form.appendChild(actions);
    container.prepend(form);
  }

  // ── Catalogs ───────────────────────────────────────────

  private async renderCatalogs(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const [catalogSources, links] = await Promise.all([fetchCatalogSources(), fetchCatalogs()]);
      el.innerHTML = '';

      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = btn('+ Add Catalog', 'hub-admin-btn hub-admin-btn--primary');
      addBtn.addEventListener('click', () => this.showAddCatalogForm(el));
      toolbar.appendChild(addBtn);
      el.appendChild(toolbar);

      if (catalogSources.length === 0) {
        el.appendChild(empty('No catalog sources'));
        return;
      }

      for (const cs of catalogSources) {
        const dsLinks = links.filter(l => l.catalog_name === cs.name);
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';
        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-list-item-title">${esc(cs.name)}</span>
            <span class="hub-admin-list-item-meta">${esc(cs.type)} — ${dsLinks.map(l => esc(l.data_source_name)).join(', ') || 'unlinked'}</span>
          </div>
        `;
        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';
        const delBtn = btn('Delete', 'hub-admin-btn-sm hub-admin-btn--danger');
        delBtn.addEventListener('click', async () => { await deleteCatalogSource(cs.name); this.loadSection('catalogs'); });
        actions.appendChild(delBtn);
        row.appendChild(actions);
        el.appendChild(row);
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddCatalogForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-form';
    form.innerHTML = `
      <div class="hub-admin-form-group"><label>Name</label><input type="text" id="cat-name" /></div>
      <div class="hub-admin-form-group"><label>Type</label>
        <select id="cat-type"><option value="localFS">localFS</option><option value="uri">uri</option><option value="text">text</option></select>
      </div>
      <div class="hub-admin-form-group"><label>Path</label><input type="text" id="cat-path" placeholder="/schemas/ or s3://..." /></div>
      <div class="hub-admin-form-group"><label>Description</label><input type="text" id="cat-desc" /></div>
      <div class="hub-admin-form-group"><label>Link to data source (optional)</label><input type="text" id="cat-ds" placeholder="data source name" /></div>
    `;
    const actions = document.createElement('div');
    actions.className = 'hub-admin-form-actions';
    const saveBtn = btn('Create', 'hub-admin-btn hub-admin-btn--primary');
    saveBtn.addEventListener('click', async () => {
      const name = (form.querySelector('#cat-name') as HTMLInputElement).value;
      const type = (form.querySelector('#cat-type') as HTMLSelectElement).value;
      const path = (form.querySelector('#cat-path') as HTMLInputElement).value;
      const desc = (form.querySelector('#cat-desc') as HTMLInputElement).value;
      const dsName = (form.querySelector('#cat-ds') as HTMLInputElement).value;
      if (!name || !path) return;
      await insertCatalogSource({ name, type, path, description: desc || undefined });
      if (dsName) await linkCatalog(name, dsName);
      this.loadSection('catalogs');
    });
    const cancelBtn = btn('Cancel', 'hub-admin-btn');
    cancelBtn.addEventListener('click', () => this.loadSection('catalogs'));
    actions.appendChild(saveBtn);
    actions.appendChild(cancelBtn);
    form.appendChild(actions);
    container.prepend(form);
  }

  // ── Budgets ────────────────────────────────────────────

  private async renderBudgets(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const budgets = await fetchLLMBudgets();
      el.innerHTML = '';

      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = btn('+ Add Budget', 'hub-admin-btn hub-admin-btn--primary');
      addBtn.addEventListener('click', () => this.showAddBudgetForm(el));
      toolbar.appendChild(addBtn);
      el.appendChild(toolbar);

      if (budgets.length === 0) {
        el.appendChild(empty('No budgets configured'));
        return;
      }

      for (const b of budgets) {
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';
        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-list-item-title">${esc(b.scope)}${b.provider_id ? ' (' + esc(b.provider_id) + ')' : ''}</span>
            <span class="hub-admin-list-item-meta">${esc(b.period)} — in:${fmtTokens(b.max_tokens_in ?? 0)} out:${fmtTokens(b.max_tokens_out ?? 0)}, ${b.max_requests ?? 0} reqs</span>
          </div>
        `;
        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';
        const delBtn = btn('Delete', 'hub-admin-btn-sm hub-admin-btn--danger');
        delBtn.addEventListener('click', async () => { await deleteLLMBudget(b.id); this.loadSection('budgets'); });
        actions.appendChild(delBtn);
        row.appendChild(actions);
        el.appendChild(row);
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private showAddBudgetForm(container: HTMLElement): void {
    const form = document.createElement('div');
    form.className = 'hub-admin-form';
    form.innerHTML = `
      <div class="hub-admin-form-group"><label>Scope (e.g. global, user:alice, role:analyst)</label><input type="text" id="bud-scope" value="global" /></div>
      <div class="hub-admin-form-group"><label>Model name (empty = all)</label><input type="text" id="bud-provider" /></div>
      <div class="hub-admin-form-group"><label>Period</label><select id="bud-period"><option value="hour">Hour</option><option value="day">Day</option><option value="month">Month</option></select></div>
      <div class="hub-admin-form-group"><label>Max Input Tokens</label><input type="number" id="bud-tin" value="1000000" /></div>
      <div class="hub-admin-form-group"><label>Max Output Tokens</label><input type="number" id="bud-tout" value="500000" /></div>
      <div class="hub-admin-form-group"><label>Max Requests</label><input type="number" id="bud-reqs" value="1000" /></div>
    `;
    const actions = document.createElement('div');
    actions.className = 'hub-admin-form-actions';
    const saveBtn = btn('Create', 'hub-admin-btn hub-admin-btn--primary');
    saveBtn.addEventListener('click', async () => {
      const scope = (form.querySelector('#bud-scope') as HTMLInputElement).value;
      const providerId = (form.querySelector('#bud-provider') as HTMLInputElement).value;
      const period = (form.querySelector('#bud-period') as HTMLSelectElement).value;
      const maxIn = parseInt((form.querySelector('#bud-tin') as HTMLInputElement).value, 10);
      const maxOut = parseInt((form.querySelector('#bud-tout') as HTMLInputElement).value, 10);
      const maxReqs = parseInt((form.querySelector('#bud-reqs') as HTMLInputElement).value, 10);
      await insertLLMBudget({ scope, provider_id: providerId || null as any, period, max_tokens_in: maxIn, max_tokens_out: maxOut, max_requests: maxReqs });
      this.loadSection('budgets');
    });
    const cancelBtn = btn('Cancel', 'hub-admin-btn');
    cancelBtn.addEventListener('click', () => this.loadSection('budgets'));
    actions.appendChild(saveBtn);
    actions.appendChild(cancelBtn);
    form.appendChild(actions);
    container.prepend(form);
  }

  // ── Agents ─────────────────────────────────────────────

  private async renderAgents(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const instances = await fetchAgentInstances();
      el.innerHTML = '';
      if (instances.length === 0) { el.appendChild(empty('No agent instances')); return; }

      for (const inst of instances) {
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';
        const dot = inst.status === 'running' ? 'hub-admin-dot--active' : inst.status === 'error' ? 'hub-admin-dot--error' : 'hub-admin-dot--inactive';
        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-dot ${dot}"></span>
            <span class="hub-admin-list-item-title">${esc(inst.user_id)}</span>
            <span class="hub-admin-list-item-meta">${esc(inst.agent_type_id)} — ${fmtDate(inst.started_at)}</span>
          </div>
        `;
        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';
        if (inst.status === 'running') {
          const stopBtn = btn('Stop', 'hub-admin-btn-sm hub-admin-btn--danger');
          stopBtn.addEventListener('click', async () => { await stopAgent(inst.id); this.loadSection('agents'); });
          actions.appendChild(stopBtn);
        }
        const clearBtn = btn('Clear Memory', 'hub-admin-btn-sm');
        clearBtn.addEventListener('click', async () => { await clearAgentMemory(inst.user_id); this.loadSection('agents'); });
        actions.appendChild(clearBtn);
        row.appendChild(actions);
        el.appendChild(row);
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }
}

// ── Helpers ──────────────────────────────────────────────

function subheading(text: string): HTMLElement {
  const h = document.createElement('div');
  h.className = 'hub-admin-subheading';
  h.textContent = text;
  return h;
}

function empty(text: string): HTMLElement {
  const div = document.createElement('div');
  div.className = 'hub-admin-empty';
  div.textContent = text;
  return div;
}

function btn(text: string, cls: string): HTMLButtonElement {
  const b = document.createElement('button');
  b.className = cls;
  b.textContent = text;
  return b;
}

function table(headers: string[], rows: string[][]): HTMLTableElement {
  const t = document.createElement('table');
  t.className = 'hub-admin-table';
  const thead = document.createElement('thead');
  const hr = document.createElement('tr');
  for (const h of headers) { const th = document.createElement('th'); th.textContent = h; hr.appendChild(th); }
  thead.appendChild(hr);
  t.appendChild(thead);
  const tbody = document.createElement('tbody');
  for (const row of rows) {
    const tr = document.createElement('tr');
    for (const cell of row) { const td = document.createElement('td'); td.textContent = cell; tr.appendChild(td); }
    tbody.appendChild(tr);
  }
  t.appendChild(tbody);
  return t;
}

function fmtDate(iso: string): string {
  if (!iso) return '-';
  return new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function esc(s: string): string {
  const div = document.createElement('div');
  div.textContent = s ?? '';
  return div.innerHTML;
}
