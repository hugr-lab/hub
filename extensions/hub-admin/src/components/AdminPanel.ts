/**
 * Hub Admin Panel — sidebar widget with collapsible sections.
 * Data Sources and Catalogs use AG Grid for tables.
 */
import { Widget } from '@lumino/widgets';
import { createGrid, GridApi, GridOptions, themeQuartz } from 'ag-grid-community';

import {
  fetchDataSources, fetchDataSourceStatus, insertDataSource, updateDataSource, deleteDataSource,
  loadDataSource, unloadDataSource,
  fetchCatalogSources, fetchCatalogs, insertCatalogSource, updateCatalogSource,
  deleteCatalogSource, linkCatalog, unlinkCatalog,
  fetchModelSources,
  fetchAgentSessions, fetchAgentTypes,
  clearAgentMemory,
  fetchLLMBudgets, fetchLLMUsage, insertLLMBudget, deleteLLMBudget,
} from '../api.js';
// Agent CRUD + lifecycle via direct Hugr GraphQL.
import {
  fetchAgents, createAgent, startAgent, stopAgent, renameAgent, deleteAgent,
} from '../agentApiGraphQL.js';
import type { DataSource, CatalogSource, CatalogLink, ModelSource, AgentType } from '../api.js';
import { ICON, iconBtn } from './icons.js';
import { Modal } from './modal.js';

type Section = 'dashboard' | 'datasources' | 'catalogs' | 'budgets' | 'agents';

const SECTIONS: { key: Section; label: string }[] = [
  { key: 'dashboard', label: 'DASHBOARD' },
  { key: 'datasources', label: 'DATA SOURCES' },
  { key: 'catalogs', label: 'CATALOGS' },
  { key: 'budgets', label: 'BUDGETS' },
  { key: 'agents', label: 'AGENTS' },
];

const DS_TYPES = [
  'postgres', 'duckdb', 'mysql', 'mssql',
  'llm-openai', 'llm-anthropic', 'llm-gemini',
  'redis', 'http', 'embedding', 'extension', 'airport', 'ducklake', 'iceberg',
];

export class AdminPanelWidget extends Widget {
  private expanded: Record<Section, boolean> = {
    dashboard: true, datasources: false, catalogs: false, budgets: false, agents: false,
  };
  private sectionBodies: Record<Section, HTMLDivElement> = {} as any;
  private sectionHeaders: Record<Section, HTMLDivElement> = {} as any;
  // Cache for cross-section data
  private _catalogLinks: CatalogLink[] = [];
  private _models: ModelSource[] = [];
  private _busy = false;

  constructor() {
    super();
    this.id = 'hub-admin-panel';
    this.title.closable = true;
    this.addClass('hub-admin-panel');
    this.buildLayout();
  }

  /** Run an async operation with a spinner overlay. Prevents double-clicks. */
  private async runBusy<T>(label: string, fn: () => Promise<T>): Promise<T | undefined> {
    if (this._busy) return undefined;
    this._busy = true;
    const overlay = document.createElement('div');
    overlay.className = 'hub-admin-busy-overlay';
    overlay.innerHTML = `<div class="hub-admin-busy-spinner"></div><div>${label}</div>`;
    this.node.appendChild(overlay);
    try {
      return await fn();
    } finally {
      this._busy = false;
      overlay.remove();
    }
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
      if (this.expanded[section.key]) header.classList.add('hub-admin-section-header--expanded');
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
      const [agents, sessions, usage, models] = await Promise.all([
        fetchAgents(), fetchAgentSessions(), fetchLLMUsage(1000), fetchModelSources(),
      ]);
      el.innerHTML = '';
      const running = agents.filter(a => a.status === 'running').length;
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
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  // ── Data Sources (AG Grid) ─────────────────────────────

  private async renderDataSources(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const [sources, models, links] = await Promise.all([
        fetchDataSources(), fetchModelSources(), fetchCatalogs(),
      ]);
      this._catalogLinks = links;
      this._models = models;
      el.innerHTML = '';

      // Toolbar
      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = iconBtn(ICON.plus, 'Add data source', 'hub-admin-icon-btn hub-admin-icon-btn--primary');
      addBtn.addEventListener('click', () => this.openDataSourceModal(null));
      toolbar.appendChild(addBtn);
      const refreshBtn = iconBtn(ICON.refresh, 'Refresh');
      refreshBtn.addEventListener('click', () => this.loadSection('datasources'));
      toolbar.appendChild(refreshBtn);
      el.appendChild(toolbar);

      // Grid container
      const gridDiv = document.createElement('div');
      gridDiv.className = 'hub-admin-grid';
      gridDiv.style.height = `${Math.min(sources.length * 32 + 34, 400)}px`;
      el.appendChild(gridDiv);

      // Fetch statuses in parallel
      const statuses = await Promise.all(
        sources.map(ds => fetchDataSourceStatus(ds.name).catch(() => 'unknown'))
      );
      const rowData = sources.map((ds, i) => ({
        ...ds,
        status: statuses[i],
        catalogs: links.filter(l => l.data_source_name === ds.name).length,
      }));

      createGrid(gridDiv, {
        theme: themeQuartz,
        rowData,
        columnDefs: [
          {
            headerName: '',
            field: 'status',
            width: 28,
            minWidth: 28,
            maxWidth: 28,
            cellRenderer: (p: any) => {
              const s = p.value as string;
              const cls = s === 'attached' ? 'hub-admin-dot--active'
                : s === 'detached' ? 'hub-admin-dot--inactive'
                : 'hub-admin-dot--error';
              return `<span class="hub-admin-dot ${cls}"></span>`;
            },
          },
          { headerName: 'Name', field: 'name', minWidth: 100, filter: true, sortable: true },
          { headerName: 'Type', field: 'type', minWidth: 70, width: 90, filter: true, sortable: true },
          { headerName: 'Description', field: 'description', minWidth: 80 },
          { headerName: 'Cat', field: 'catalogs', width: 45, minWidth: 45, maxWidth: 45, sortable: true },
          {
            headerName: '',
            width: 80,
            cellRenderer: (p: any) => {
              const div = document.createElement('div');
              div.className = 'hub-admin-grid-actions';
              const isDetached = p.data.status !== 'attached';
              const toggleBtn = iconBtn(isDetached ? ICON.play : ICON.power, isDetached ? 'Load' : 'Unload');
              toggleBtn.addEventListener('click', async (e) => {
                e.stopPropagation();
                if (!isDetached && !confirm(`Unload data source "${p.data.name}"?`)) return;
                await this.runBusy(isDetached ? `Loading ${p.data.name}...` : `Unloading ${p.data.name}...`, async () => {
                  try {
                    const res = isDetached
                      ? await loadDataSource(p.data.name)
                      : await unloadDataSource(p.data.name);
                    if (!res.success && res.message) { alert(res.message); }
                  } catch (err: any) { alert(err.message); }
                });
                this.loadSection('datasources');
              });
              div.appendChild(toggleBtn);
              const delB = iconBtn(ICON.trash, 'Delete', 'hub-admin-icon-btn hub-admin-icon-btn--danger');
              delB.addEventListener('click', async (e) => {
                e.stopPropagation();
                if (!confirm(`Delete data source "${p.data.name}"?`)) return;
                await deleteDataSource(p.data.name);
                this.loadSection('datasources');
              });
              div.appendChild(delB);
              return div;
            },
          },
        ],
        headerHeight: 28,
        rowHeight: 32,
        suppressCellFocus: true,
        domLayout: 'normal',
        onRowClicked: (e: any) => { if (e.data) this.openDataSourceModal(e.data); },
      });
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private async openDataSourceModal(existing: (DataSource & { catalogs?: number }) | null): Promise<void> {
    const isEdit = !!existing;
    const modal = new Modal({
      title: isEdit ? `Edit: ${existing!.name}` : 'New Data Source',
      width: '500px',
    });

    // Fetch linked catalogs for edit mode
    let linkedCatalogs: CatalogLink[] = [];
    let allCatalogSources: CatalogSource[] = [];
    if (isEdit) {
      linkedCatalogs = this._catalogLinks.filter(l => l.data_source_name === existing!.name);
      allCatalogSources = await fetchCatalogSources();
    }

    const form = document.createElement('div');
    form.className = 'hub-admin-modal-form';
    form.innerHTML = `
      <div class="hub-admin-form-group"><label>Name</label><input type="text" id="m-name" value="${esc(existing?.name ?? '')}" ${isEdit ? 'disabled' : ''} /></div>
      <div class="hub-admin-form-group"><label>Type</label>
        <select id="m-type" ${isEdit ? 'disabled' : ''}>${DS_TYPES.map(t => `<option value="${t}" ${existing?.type === t ? 'selected' : ''}>${t}</option>`).join('')}</select>
      </div>
      <div class="hub-admin-form-group"><label>Prefix</label><input type="text" id="m-prefix" value="${esc(existing?.prefix ?? '')}" /></div>
      <div class="hub-admin-form-group"><label>Path</label><textarea id="m-path" rows="3" style="resize:vertical;font-family:monospace;font-size:12px">${esc(existing?.path ?? '')}</textarea></div>
      <div class="hub-admin-form-group"><label>Description</label><input type="text" id="m-desc" value="${esc(existing?.description ?? '')}" /></div>
      <div class="hub-admin-form-row">
        <label><input type="checkbox" id="m-readonly" ${existing?.read_only ? 'checked' : ''} /> Read Only</label>
        <label><input type="checkbox" id="m-module" ${existing?.as_module ? 'checked' : ''} /> As Module</label>
        <label><input type="checkbox" id="m-self" ${existing?.self_defined ? 'checked' : ''} /> Self Defined</label>
        <label><input type="checkbox" id="m-disabled" ${existing?.disabled ? 'checked' : ''} /> Disabled</label>
      </div>
    `;

    // Auto-fill prefix from name
    if (!isEdit) {
      const nameInput = form.querySelector('#m-name') as HTMLInputElement;
      const prefixInput = form.querySelector('#m-prefix') as HTMLInputElement;
      nameInput.addEventListener('input', () => {
        prefixInput.value = nameInput.value.replace(/[.\-]/g, '_');
      });
    }

    modal.body.appendChild(form);

    // ── Catalogs sub-section ──
    const catSection = document.createElement('div');
    catSection.className = 'hub-admin-modal-subsection';
    const catHeader = document.createElement('div');
    catHeader.className = 'hub-admin-modal-subsection-header';
    catHeader.innerHTML = '<strong>Catalogs</strong>';
    const addCatBtn = iconBtn(ICON.plus, 'Add catalog', 'hub-admin-icon-btn hub-admin-icon-btn--small');
    catHeader.appendChild(addCatBtn);
    catSection.appendChild(catHeader);

    const catTable = document.createElement('div');
    catTable.className = 'hub-admin-modal-cat-table';
    catSection.appendChild(catTable);
    modal.body.appendChild(catSection);

    // Catalog rows state
    type CatRow = { name: string; type: string; path: string; isNew: boolean; isLinked: boolean };
    const catRows: CatRow[] = [];
    if (isEdit) {
      for (const lnk of linkedCatalogs) {
        const cs = allCatalogSources.find(c => c.name === lnk.catalog_name);
        catRows.push({
          name: lnk.catalog_name,
          type: cs?.type ?? '',
          path: cs?.path ?? '',
          isNew: false,
          isLinked: true,
        });
      }
    }

    const renderCatRows = () => {
      catTable.innerHTML = '';
      if (catRows.length === 0) {
        catTable.innerHTML = '<div class="hub-admin-empty" style="padding:4px">No catalogs</div>';
        return;
      }
      for (let i = 0; i < catRows.length; i++) {
        const row = catRows[i];
        const rowEl = document.createElement('div');
        rowEl.className = 'hub-admin-modal-cat-row';
        rowEl.innerHTML = `
          <div style="flex:1;min-width:0">
            <span class="hub-admin-modal-cat-name">${esc(row.name)}</span>
            <span class="hub-admin-modal-cat-meta">${esc(row.type)}</span>
            <div class="hub-admin-modal-cat-meta" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(row.path)}">${esc(row.path)}</div>
          </div>
        `;
        const delCatBtn = iconBtn(ICON.trash, row.isLinked ? 'Unlink' : 'Remove', 'hub-admin-icon-btn hub-admin-icon-btn--danger hub-admin-icon-btn--small');
        delCatBtn.addEventListener('click', () => {
          catRows.splice(i, 1);
          renderCatRows();
        });
        rowEl.appendChild(delCatBtn);
        catTable.appendChild(rowEl);
      }
    };
    renderCatRows();

    addCatBtn.addEventListener('click', () => {
      const catForm = document.createElement('div');
      catForm.className = 'hub-admin-modal-cat-add';
      catForm.innerHTML = `
        <input type="text" id="mc-name" placeholder="Catalog name" style="flex:2" />
        <select id="mc-type" style="flex:1"><option value="localFS">localFS</option><option value="uri">uri</option><option value="uriFile">uriFile</option><option value="text">text</option></select>
        <input type="text" id="mc-path" placeholder="Path" style="flex:3" />
      `;
      const okBtn = iconBtn(ICON.check, 'Add', 'hub-admin-icon-btn hub-admin-icon-btn--small');
      okBtn.addEventListener('click', () => {
        const cName = (catForm.querySelector('#mc-name') as HTMLInputElement).value;
        const cType = (catForm.querySelector('#mc-type') as HTMLSelectElement).value;
        const cPath = (catForm.querySelector('#mc-path') as HTMLInputElement).value;
        if (!cName || !cPath) return;
        catRows.push({ name: cName, type: cType, path: cPath, isNew: true, isLinked: false });
        catForm.remove();
        renderCatRows();
      });
      const cancelBtn = iconBtn(ICON.x, 'Cancel', 'hub-admin-icon-btn hub-admin-icon-btn--small');
      cancelBtn.addEventListener('click', () => catForm.remove());
      catForm.appendChild(okBtn);
      catForm.appendChild(cancelBtn);
      catTable.prepend(catForm);
    });

    // ── Actions ──
    if (isEdit) {
      modal.addAction('Load', 'hub-admin-btn', async () => {
        modal.close();
        await this.runBusy(`Loading ${existing!.name}...`, async () => {
          try {
            const res = await loadDataSource(existing!.name);
            if (res.message) { alert(res.success ? `✓ ${res.message}` : res.message); }
          } catch (err: any) { alert(err.message); }
        });
        this.loadSection('datasources');
      });
      modal.addAction('Unload', 'hub-admin-btn', async () => {
        if (!confirm(`Unload data source "${existing!.name}"?`)) return;
        modal.close();
        await this.runBusy(`Unloading ${existing!.name}...`, async () => {
          try {
            const res = await unloadDataSource(existing!.name);
            if (res.message) { alert(res.success ? `✓ ${res.message}` : res.message); }
          } catch (err: any) { alert(err.message); }
        });
        this.loadSection('datasources');
      });
      modal.addAction('Hard Unload', 'hub-admin-btn hub-admin-icon-btn--danger', async () => {
        if (!confirm(`Hard unload "${existing!.name}"? This will completely remove the data source.`)) return;
        modal.close();
        await this.runBusy(`Hard unloading ${existing!.name}...`, async () => {
          try {
            const res = await unloadDataSource(existing!.name, true);
            if (res.message) { alert(res.success ? `✓ ${res.message}` : res.message); }
          } catch (err: any) { alert(err.message); }
        });
        this.loadSection('datasources');
      });
    }
    modal.addAction(isEdit ? 'Save' : 'Create', 'hub-admin-btn hub-admin-btn--primary', async () => {
      const name = (form.querySelector('#m-name') as HTMLInputElement).value;
      const type = (form.querySelector('#m-type') as HTMLSelectElement).value;
      const prefix = (form.querySelector('#m-prefix') as HTMLInputElement).value;
      const path = (form.querySelector('#m-path') as HTMLTextAreaElement).value;
      const desc = (form.querySelector('#m-desc') as HTMLInputElement).value;
      const readOnly = (form.querySelector('#m-readonly') as HTMLInputElement).checked;
      const asModule = (form.querySelector('#m-module') as HTMLInputElement).checked;
      const selfDefined = (form.querySelector('#m-self') as HTMLInputElement).checked;
      const disabled = (form.querySelector('#m-disabled') as HTMLInputElement).checked;

      if (!name || !path) return;

      try {
        if (isEdit) {
          await updateDataSource(name, { prefix, path, description: desc, read_only: readOnly, as_module: asModule, self_defined: selfDefined, disabled } as any);
          // Handle catalog changes
          const oldNames = new Set(linkedCatalogs.map(l => l.catalog_name));
          const newNames = new Set(catRows.map(r => r.name));
          // Unlink removed
          for (const old of linkedCatalogs) {
            if (!newNames.has(old.catalog_name)) {
              await unlinkCatalog(old.catalog_name, name);
            }
          }
          // Create new catalogs and link
          for (const row of catRows) {
            if (row.isNew) {
              await insertCatalogSource({ name: row.name, type: row.type, path: row.path });
              await linkCatalog(row.name, name);
            } else if (!oldNames.has(row.name)) {
              await linkCatalog(row.name, name);
            }
          }
        } else {
          const newCats = catRows.filter(r => r.isNew).map(r => ({ name: r.name, type: r.type, path: r.path }));
          await insertDataSource(
            { name, type, prefix: prefix || name.replace(/[.\-]/g, '_'), path, description: desc, read_only: readOnly, as_module: asModule, self_defined: selfDefined, disabled } as any,
            newCats.length > 0 ? newCats : undefined,
          );
        }
        modal.close();
        this.loadSection('datasources');
      } catch (err: any) {
        alert(err.message);
      }
    });
    modal.addAction('Cancel', 'hub-admin-btn', () => modal.close());
    modal.open();
  }

  // ── Catalogs (AG Grid) ─────────────────────────────────

  private async renderCatalogs(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const [catalogSources, links] = await Promise.all([fetchCatalogSources(), fetchCatalogs()]);
      el.innerHTML = '';

      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = iconBtn(ICON.plus, 'Add catalog', 'hub-admin-icon-btn hub-admin-icon-btn--primary');
      addBtn.addEventListener('click', () => this.openCatalogModal(null, links));
      toolbar.appendChild(addBtn);
      const refreshBtn = iconBtn(ICON.refresh, 'Refresh');
      refreshBtn.addEventListener('click', () => this.loadSection('catalogs'));
      toolbar.appendChild(refreshBtn);
      el.appendChild(toolbar);

      const gridDiv = document.createElement('div');
      gridDiv.className = 'hub-admin-grid';
      gridDiv.style.height = `${Math.min(catalogSources.length * 32 + 34, 400)}px`;
      el.appendChild(gridDiv);

      createGrid(gridDiv, {
        theme: themeQuartz,
        rowData: catalogSources.map(cs => ({
          ...cs,
          linkedDS: links.filter(l => l.catalog_name === cs.name).map(l => l.data_source_name).join(', '),
        })),
        columnDefs: [
          { headerName: 'Name', field: 'name', minWidth: 80, filter: true, sortable: true },
          { headerName: 'Type', field: 'type', width: 70, minWidth: 60, filter: true, sortable: true },
          { headerName: 'Description', field: 'description', minWidth: 80 },
          { headerName: 'DS', field: 'linkedDS', minWidth: 60 },
          {
            headerName: '',
            width: 50,
            cellRenderer: (p: any) => {
              const div = document.createElement('div');
              div.className = 'hub-admin-grid-actions';
              const delB = iconBtn(ICON.trash, 'Delete', 'hub-admin-icon-btn hub-admin-icon-btn--danger');
              delB.addEventListener('click', async (e) => {
                e.stopPropagation();
                if (!confirm(`Delete catalog "${p.data.name}"?`)) return;
                await deleteCatalogSource(p.data.name);
                this.loadSection('catalogs');
              });
              div.appendChild(delB);
              return div;
            },
          },
        ],
        headerHeight: 28,
        rowHeight: 32,
        suppressCellFocus: true,
        domLayout: 'normal',
        onRowClicked: (e: any) => { if (e.data) this.openCatalogModal(e.data, links); },
      });
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private openCatalogModal(existing: CatalogSource | null, links: CatalogLink[]): void {
    const isEdit = !!existing;
    const modal = new Modal({ title: isEdit ? `Edit: ${existing!.name}` : 'New Catalog', width: '450px' });

    const form = document.createElement('div');
    form.className = 'hub-admin-modal-form';
    form.innerHTML = `
      <div class="hub-admin-form-group"><label>Name</label><input type="text" id="mc-name" value="${esc(existing?.name ?? '')}" ${isEdit ? 'disabled' : ''} /></div>
      <div class="hub-admin-form-group"><label>Type</label>
        <select id="mc-type"><option value="localFS" ${existing?.type === 'localFS' ? 'selected' : ''}>localFS</option><option value="uri" ${existing?.type === 'uri' ? 'selected' : ''}>uri</option><option value="text" ${existing?.type === 'text' ? 'selected' : ''}>text</option></select>
      </div>
      <div class="hub-admin-form-group"><label>Path</label><textarea id="mc-path" rows="3" style="resize:vertical;font-family:monospace;font-size:12px">${esc(existing?.path ?? '')}</textarea></div>
      <div class="hub-admin-form-group"><label>Description</label><input type="text" id="mc-desc" value="${esc(existing?.description ?? '')}" /></div>
      <div class="hub-admin-form-group"><label>Link to data source</label><input type="text" id="mc-ds" placeholder="data source name" /></div>
    `;
    modal.body.appendChild(form);

    // Show linked data sources for existing catalogs
    if (isEdit) {
      const dsLinks = links.filter(l => l.catalog_name === existing!.name);
      if (dsLinks.length > 0) {
        const linkDiv = document.createElement('div');
        linkDiv.className = 'hub-admin-modal-subsection';
        linkDiv.innerHTML = '<strong>Linked data sources</strong>';
        for (const lnk of dsLinks) {
          const row = document.createElement('div');
          row.className = 'hub-admin-modal-cat-row';
          row.innerHTML = `<span>${esc(lnk.data_source_name)}</span>`;
          const unlinkBtn = iconBtn(ICON.unlink, 'Unlink', 'hub-admin-icon-btn hub-admin-icon-btn--danger hub-admin-icon-btn--small');
          unlinkBtn.addEventListener('click', async () => {
            await unlinkCatalog(lnk.catalog_name, lnk.data_source_name);
            modal.close();
            this.loadSection('catalogs');
          });
          row.appendChild(unlinkBtn);
          linkDiv.appendChild(row);
        }
        modal.body.appendChild(linkDiv);
      }
    }

    modal.addAction(isEdit ? 'Save' : 'Create', 'hub-admin-btn hub-admin-btn--primary', async () => {
      const name = (form.querySelector('#mc-name') as HTMLInputElement).value;
      const type = (form.querySelector('#mc-type') as HTMLSelectElement).value;
      const path = (form.querySelector('#mc-path') as HTMLTextAreaElement).value;
      const desc = (form.querySelector('#mc-desc') as HTMLInputElement).value;
      const dsName = (form.querySelector('#mc-ds') as HTMLInputElement).value;
      if (!name || !path) return;
      try {
        if (isEdit) {
          await updateCatalogSource(name, { type, path, description: desc } as any);
        } else {
          await insertCatalogSource({ name, type, path, description: desc || undefined });
        }
        if (dsName) await linkCatalog(name, dsName);
        modal.close();
        this.loadSection('catalogs');
      } catch (err: any) { alert(err.message); }
    });
    modal.addAction('Cancel', 'hub-admin-btn', () => modal.close());
    modal.open();
  }

  // ── Budgets ────────────────────────────────────────────

  private async renderBudgets(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const budgets = await fetchLLMBudgets();
      el.innerHTML = '';
      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      const addBtn = iconBtn(ICON.plus, 'Add budget', 'hub-admin-icon-btn hub-admin-icon-btn--primary');
      addBtn.addEventListener('click', () => this.openBudgetModal());
      toolbar.appendChild(addBtn);
      el.appendChild(toolbar);

      if (budgets.length === 0) { el.appendChild(empty('No budgets configured')); return; }

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
        const delBtn = iconBtn(ICON.trash, 'Delete', 'hub-admin-icon-btn hub-admin-icon-btn--danger');
        delBtn.addEventListener('click', async () => { await deleteLLMBudget(b.id); this.loadSection('budgets'); });
        actions.appendChild(delBtn);
        row.appendChild(actions);
        el.appendChild(row);
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private openBudgetModal(): void {
    const modal = new Modal({ title: 'New Budget', width: '400px' });
    const form = document.createElement('div');
    form.className = 'hub-admin-modal-form';
    form.innerHTML = `
      <div class="hub-admin-form-group"><label>Scope</label><input type="text" id="mb-scope" value="global" /></div>
      <div class="hub-admin-form-group"><label>Model (empty = all)</label><input type="text" id="mb-prov" /></div>
      <div class="hub-admin-form-group"><label>Period</label><select id="mb-period"><option value="hour">Hour</option><option value="day">Day</option><option value="month">Month</option></select></div>
      <div class="hub-admin-form-group"><label>Max Input Tokens</label><input type="number" id="mb-tin" value="1000000" /></div>
      <div class="hub-admin-form-group"><label>Max Output Tokens</label><input type="number" id="mb-tout" value="500000" /></div>
      <div class="hub-admin-form-group"><label>Max Requests</label><input type="number" id="mb-reqs" value="1000" /></div>
    `;
    modal.body.appendChild(form);
    modal.addAction('Create', 'hub-admin-btn hub-admin-btn--primary', async () => {
      const scope = (form.querySelector('#mb-scope') as HTMLInputElement).value;
      const prov = (form.querySelector('#mb-prov') as HTMLInputElement).value;
      const period = (form.querySelector('#mb-period') as HTMLSelectElement).value;
      const tin = parseInt((form.querySelector('#mb-tin') as HTMLInputElement).value, 10);
      const tout = parseInt((form.querySelector('#mb-tout') as HTMLInputElement).value, 10);
      const reqs = parseInt((form.querySelector('#mb-reqs') as HTMLInputElement).value, 10);
      try {
        await insertLLMBudget({ scope, provider_id: prov || null as any, period, max_tokens_in: tin, max_tokens_out: tout, max_requests: reqs });
        modal.close(); this.loadSection('budgets');
      } catch (err: any) { alert(err.message); }
    });
    modal.addAction('Cancel', 'hub-admin-btn', () => modal.close());
    modal.open();
  }

  // ── Agents ─────────────────────────────────────────────

  /** Fetch connected agent instance IDs from hub-service via hub-chat proxy. */
  private async fetchConnectedAgents(): Promise<Set<string>> {
    try {
      const baseUrl = (await import('@jupyterlab/coreutils')).PageConfig.getBaseUrl();
      const settings = (await import('@jupyterlab/services')).ServerConnection.makeSettings();
      const resp = await (await import('@jupyterlab/services')).ServerConnection.makeRequest(
        baseUrl + 'hub-chat/api/agent/instances', {}, settings,
      );
      if (!resp.ok) return new Set();
      const data = await resp.json();
      const ids = new Set<string>();
      if (Array.isArray(data)) {
        for (const inst of data) {
          if (inst.connected) ids.add(inst.id);
        }
      }
      return ids;
    } catch {
      return new Set();
    }
  }

  private async renderAgents(el: HTMLElement): Promise<void> {
    el.innerHTML = '<div class="hub-admin-loading">Loading...</div>';
    try {
      const [agents, agentTypes, connectedSet] = await Promise.all([
        fetchAgents(),
        fetchAgentTypes(),
        this.fetchConnectedAgents(),
      ]);
      el.innerHTML = '';

      // Toolbar: Create Agent
      const toolbar = document.createElement('div');
      toolbar.className = 'hub-admin-toolbar';
      if (agentTypes.length > 0) {
        const createBtn = iconBtn(ICON.plus, 'Create Agent', 'hub-admin-icon-btn hub-admin-icon-btn--primary');
        createBtn.addEventListener('click', () => this.openCreateAgentModal(agentTypes));
        toolbar.appendChild(createBtn);
      }
      const refreshBtn = iconBtn(ICON.refresh, 'Refresh');
      refreshBtn.addEventListener('click', () => this.loadSection('agents'));
      toolbar.appendChild(refreshBtn);
      el.appendChild(toolbar);

      if (agents.length === 0) { el.appendChild(empty('No agents — click Create Agent to add one')); return; }
      for (const agent of agents) {
        const row = document.createElement('div');
        row.className = 'hub-admin-list-item';
        const isRunning = agent.status === 'running';
        const isConnected = isRunning && connectedSet.has(agent.id);
        const dot = isConnected ? 'hub-admin-dot--active' :
          isRunning ? 'hub-admin-dot--warning' :
          agent.status === 'error' ? 'hub-admin-dot--error' : 'hub-admin-dot--inactive';
        const statusLabel = isRunning ? (isConnected ? 'connected' : 'disconnected') : (agent.status || 'stopped');
        const meta = isRunning && agent.started_at
          ? `${statusLabel} — since ${fmtDate(agent.started_at)} — ${esc(agent.hugr_role)}`
          : `${statusLabel} — ${esc(agent.hugr_role)}`;
        row.innerHTML = `
          <div class="hub-admin-list-item-main">
            <span class="hub-admin-dot ${dot}" title="${statusLabel}"></span>
            <span class="hub-admin-list-item-title">${esc(agent.display_name)}</span>
            <span class="hub-admin-list-item-meta">${meta}</span>
          </div>
        `;
        const actions = document.createElement('div');
        actions.className = 'hub-admin-list-item-actions';

        if (isRunning) {
          const stopBtn = iconBtn(ICON.stop, 'Stop', 'hub-admin-icon-btn hub-admin-icon-btn--danger');
          stopBtn.addEventListener('click', async () => {
            if (!confirm(`Stop agent "${agent.display_name}"?`)) return;
            await this.runBusy(`Stopping agent...`, async () => {
              try { await stopAgent(agent.id); } catch (err: any) { alert(err.message); }
            });
            this.loadSection('agents');
          });
          actions.appendChild(stopBtn);
        } else {
          const startBtn = iconBtn(ICON.play, 'Start', 'hub-admin-icon-btn hub-admin-icon-btn--primary');
          startBtn.addEventListener('click', async () => {
            await this.runBusy(`Starting agent...`, async () => {
              try { await startAgent(agent.id); } catch (err: any) { alert(err.message); }
            });
            this.loadSection('agents');
          });
          actions.appendChild(startBtn);
        }

        const renBtn = iconBtn(ICON.edit, 'Rename');
        renBtn.addEventListener('click', async () => {
          const newName = prompt('Agent display name:', agent.display_name || '');
          if (newName !== null && newName !== agent.display_name) {
            await renameAgent(agent.id, newName);
            this.loadSection('agents');
          }
        });
        actions.appendChild(renBtn);

        const clearBtn = iconBtn(ICON.eraser, 'Clear Memory');
        clearBtn.addEventListener('click', async () => {
          if (!confirm(`Clear all memory for agent "${agent.display_name}"?`)) return;
          await clearAgentMemory(agent.hugr_user_id);
          this.loadSection('agents');
        });
        actions.appendChild(clearBtn);

        const delBtn = iconBtn(ICON.trash, 'Delete', 'hub-admin-icon-btn hub-admin-icon-btn--danger');
        delBtn.addEventListener('click', async () => {
          if (!confirm(`Delete agent "${agent.display_name}"? This will stop the container and remove the record.`)) return;
          await this.runBusy('Deleting agent...', async () => {
            try { await deleteAgent(agent.id); } catch (err: any) { alert(err.message); }
          });
          this.loadSection('agents');
        });
        actions.appendChild(delBtn);
        row.appendChild(actions);
        el.appendChild(row);
      }
    } catch (err: any) {
      el.innerHTML = `<div class="hub-admin-error">${err.message}</div>`;
    }
  }

  private openCreateAgentModal(agentTypes: AgentType[]): void {
    const modal = new Modal({ title: 'Create Agent', width: '440px' });
    const form = document.createElement('div');
    form.className = 'hub-admin-modal-form';
    form.innerHTML = `
      <div class="hub-admin-form-group">
        <label>Display Name</label>
        <input type="text" id="ca-name" placeholder="e.g. My Assistant, Team Analyst" />
      </div>
      <div class="hub-admin-form-group">
        <label>Agent Type</label>
        <select id="ca-type">${agentTypes.map(t => `<option value="${t.id}">${esc(t.display_name || t.id)}</option>`).join('')}</select>
      </div>
      <div class="hub-admin-form-group">
        <label>Description <span style="color:var(--jp-ui-font-color2)">(optional)</span></label>
        <input type="text" id="ca-desc" />
      </div>
      <div class="hub-admin-form-group">
        <label>Identity</label>
        <select id="ca-identity">
          <option value="personal">Personal — inherit owner's Hugr permissions</option>
          <option value="team">Team — independent Hugr identity</option>
        </select>
      </div>
      <div class="hub-admin-form-group">
        <label>Owner User ID <span style="color:var(--jp-ui-font-color2)">(empty = current user)</span></label>
        <input type="text" id="ca-owner" placeholder="e.g. alice" />
      </div>
      <div id="ca-team-fields" style="display:none">
        <div class="hub-admin-form-group">
          <label>Hugr User ID <span style="color:var(--jp-ui-font-color2)">(team identity)</span></label>
          <input type="text" id="ca-huid" placeholder="e.g. agent-analyst-1" />
        </div>
        <div class="hub-admin-form-group">
          <label>Hugr Role</label>
          <input type="text" id="ca-role" value="agent" placeholder="e.g. analyst, agent" />
        </div>
      </div>
      <div class="hub-admin-form-group">
        <label><input type="checkbox" id="ca-start" checked /> Start agent immediately</label>
      </div>
    `;
    modal.body.appendChild(form);

    const identitySelect = form.querySelector('#ca-identity') as HTMLSelectElement;
    const teamFields = form.querySelector('#ca-team-fields') as HTMLElement;
    identitySelect.addEventListener('change', () => {
      teamFields.style.display = identitySelect.value === 'team' ? '' : 'none';
    });

    modal.addAction('Create', 'hub-admin-btn hub-admin-btn--primary', async () => {
      const displayName = (form.querySelector('#ca-name') as HTMLInputElement).value.trim();
      const typeId = (form.querySelector('#ca-type') as HTMLSelectElement).value;
      const description = (form.querySelector('#ca-desc') as HTMLInputElement).value.trim();
      const ownerUserID = (form.querySelector('#ca-owner') as HTMLInputElement).value.trim();
      const isTeam = identitySelect.value === 'team';
      const hugrUserID = isTeam ? (form.querySelector('#ca-huid') as HTMLInputElement).value.trim() : '';
      const hugrRole = isTeam ? (form.querySelector('#ca-role') as HTMLInputElement).value.trim() : '';
      const startNow = (form.querySelector('#ca-start') as HTMLInputElement).checked;

      modal.close();
      await this.runBusy('Creating agent...', async () => {
        try {
          const created = await createAgent({
            agent_type_id: typeId,
            display_name: displayName || undefined,
            description: description || undefined,
            hugr_user_id: hugrUserID || undefined,
            hugr_role: hugrRole || undefined,
            owner_user_id: ownerUserID || undefined,
          });
          if (startNow) {
            await startAgent(created.id);
          }
        } catch (err: any) { alert(err.message); }
      });
      this.loadSection('agents');
    });
    modal.addAction('Cancel', 'hub-admin-btn', () => modal.close());
    modal.open();
  }
}

// ── Helpers ──────────────────────────────────────────────

function empty(text: string): HTMLElement {
  const div = document.createElement('div');
  div.className = 'hub-admin-empty';
  div.textContent = text;
  return div;
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
