/**
 * JupyterLab Hub Chat plugin registration.
 */
import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin,
} from '@jupyterlab/application';
import { LabIcon } from '@jupyterlab/ui-components';

import { ChatPanelWidget } from './components/ChatPanel.js';

const chatIcon = new LabIcon({
  name: '@hugr-lab/jupyterlab-hub-chat:icon',
  svgstr:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>' +
    '</svg>',
});

const chatPlugin: JupyterFrontEndPlugin<void> = {
  id: '@hugr-lab/jupyterlab-hub-chat:panel',
  autoStart: true,
  activate: (app: JupyterFrontEnd) => {
    const widget = new ChatPanelWidget();
    widget.title.icon = chatIcon;
    widget.title.caption = 'Hub Chat';
    app.shell.add(widget, 'right', { rank: 50 });
  },
};

export default [chatPlugin];
