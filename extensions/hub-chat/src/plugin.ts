/**
 * JupyterLab Hub Chat plugin registration.
 */
import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin,
} from '@jupyterlab/application';

import { ChatPanelWidget } from './components/ChatPanel.js';

const chatPlugin: JupyterFrontEndPlugin<void> = {
  id: '@hugr-lab/jupyterlab-hub-chat:panel',
  autoStart: true,
  activate: (app: JupyterFrontEnd) => {
    const widget = new ChatPanelWidget();
    app.shell.add(widget, 'right', { rank: 50 });
  },
};

export default [chatPlugin];
