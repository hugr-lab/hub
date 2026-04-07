/**
 * JupyterLab Hub Admin plugin registration.
 */
import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin,
} from '@jupyterlab/application';
import { PageConfig } from '@jupyterlab/coreutils';
import { ServerConnection } from '@jupyterlab/services';
import { LabIcon } from '@jupyterlab/ui-components';

import { AdminPanelWidget } from './components/AdminPanel.js';

const adminIcon = new LabIcon({
  name: '@hugr-lab/jupyterlab-hub-admin:icon',
  svgstr:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<circle cx="12" cy="12" r="3"/>' +
    '<path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>' +
    '</svg>',
});

const adminPlugin: JupyterFrontEndPlugin<void> = {
  id: '@hugr-lab/jupyterlab-hub-admin:panel',
  autoStart: true,
  activate: async (app: JupyterFrontEnd) => {
    console.log('[hub-admin] activating...');

    // Check admin via server extension (calls JupyterHub API server-side)
    try {
      const baseUrl = PageConfig.getBaseUrl();
      const settings = ServerConnection.makeSettings();
      const checkUrl = baseUrl + 'hub-admin/api/check';
      console.log('[hub-admin] checking admin at:', checkUrl);

      const resp = await ServerConnection.makeRequest(checkUrl, {}, settings);
      if (!resp.ok) {
        console.log('[hub-admin] admin check returned:', resp.status);
        return;
      }
      const data = await resp.json();
      console.log('[hub-admin] admin check result:', data);
      if (!data.admin) return;
    } catch (err) {
      console.warn('[hub-admin] admin check failed:', err);
      return;
    }

    console.log('[hub-admin] user is admin, adding panel');
    const widget = new AdminPanelWidget();
    widget.title.icon = adminIcon;
    widget.title.caption = 'Hub Admin';
    app.shell.add(widget, 'left', { rank: 300 });
  },
};

export default [adminPlugin];
