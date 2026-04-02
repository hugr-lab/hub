/**
 * JupyterLab Hub Admin plugin registration.
 */
import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin,
} from '@jupyterlab/application';
import { ServerConnection } from '@jupyterlab/services';

import { AdminPanelWidget } from './components/AdminPanel.js';

const adminPlugin: JupyterFrontEndPlugin<void> = {
  id: '@hugr-lab/jupyterlab-hub-admin:panel',
  autoStart: true,
  activate: async (app: JupyterFrontEnd) => {
    // Only show admin panel for JupyterHub admins
    try {
      const settings = ServerConnection.makeSettings();
      const resp = await ServerConnection.makeRequest(
        settings.baseUrl + 'hub/api/user',
        {},
        settings,
      );
      if (resp.ok) {
        const user = await resp.json();
        if (!user.admin) return;
      } else {
        return;
      }
    } catch {
      return;
    }

    const widget = new AdminPanelWidget();
    app.shell.add(widget, 'left', { rank: 300 });
  },
};

export default [adminPlugin];
