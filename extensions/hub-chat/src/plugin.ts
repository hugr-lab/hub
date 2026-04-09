/**
 * Hub Chat JupyterLab Extension — conversation sidebar + main area chat widgets.
 */
import {
  JupyterFrontEnd,
  JupyterFrontEndPlugin,
} from '@jupyterlab/application';
import { MainAreaWidget } from '@jupyterlab/apputils';
import { IRenderMimeRegistry } from '@jupyterlab/rendermime';
import { LabIcon } from '@jupyterlab/ui-components';

import { ChatDocumentWidget } from './widgets/ChatDocument.js';
import { ChatSidebarWidget } from './widgets/ChatSidebar.js';

const chatIcon = new LabIcon({
  name: '@hugr-lab/jupyterlab-hub-chat:icon',
  svgstr:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>' +
    '</svg>',
});

/**
 * Opens a conversation as a main area tab. Reuses if already open.
 */
function openConversation(
  app: JupyterFrontEnd,
  conversationId: string,
  title: string,
  rendermime: IRenderMimeRegistry,
  openWidgets: Map<string, MainAreaWidget>,
  onSidebarRefresh?: () => void,
): void {
  const existing = openWidgets.get(conversationId);
  if (existing && !existing.isDisposed) {
    app.shell.activateById(existing.id);
    return;
  }

  const chatWidget = new ChatDocumentWidget(conversationId, title, rendermime);
  const main = new MainAreaWidget({ content: chatWidget });
  main.id = `hub-chat-${conversationId}`;
  main.title.label = title;
  main.title.closable = true;
  main.title.icon = chatIcon;

  // Auto-update tab title when chat generates/changes title
  chatWidget.onTitleChange = (newTitle: string) => {
    main.title.label = newTitle;
    // Refresh sidebar to show updated title
    if (onSidebarRefresh) onSidebarRefresh();
  };

  main.disposed.connect(() => {
    openWidgets.delete(conversationId);
  });

  openWidgets.set(conversationId, main);
  app.shell.add(main, 'main');
  app.shell.activateById(main.id);
}

const chatPlugin: JupyterFrontEndPlugin<void> = {
  id: '@hugr-lab/jupyterlab-hub-chat:plugin',
  autoStart: true,
  requires: [IRenderMimeRegistry],
  activate: (app: JupyterFrontEnd, rendermime: IRenderMimeRegistry) => {
    console.log('Hub Chat extension activated');

    const openWidgets = new Map<string, MainAreaWidget>();

    // Sidebar with conversation tree
    const sidebar = new ChatSidebarWidget(
      (conversationId, title) => {
        openConversation(app, conversationId, title, rendermime, openWidgets, () => sidebar.refresh());
      },
      openWidgets,
    );
    sidebar.title.icon = chatIcon;
    sidebar.title.caption = 'Hub Chat';
    app.shell.add(sidebar, 'left', { rank: 200 });
  },
};

export default [chatPlugin];
