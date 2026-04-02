/**
 * JupyterLab Hub Admin plugin registration.
 */
import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin,
} from '@jupyterlab/application';

import { AdminPanelWidget } from './components/AdminPanel.js';

const adminPlugin: JupyterFrontEndPlugin<void> = {
  id: '@hugr-lab/jupyterlab-hub-admin:panel',
  autoStart: true,
  activate: (app: JupyterFrontEnd) => {
    const widget = new AdminPanelWidget();
    app.shell.add(widget, 'left', { rank: 300 });
  },
};

export default [adminPlugin];
