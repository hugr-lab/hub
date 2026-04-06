/** Lightweight modal dialog for admin panel. */

import { ICON } from './icons.js';

export interface ModalOptions {
  title: string;
  width?: string;
}

export class Modal {
  private overlay: HTMLDivElement;
  private dialog: HTMLDivElement;
  readonly body: HTMLDivElement;
  private footer: HTMLDivElement;

  constructor(opts: ModalOptions) {
    this.overlay = document.createElement('div');
    this.overlay.className = 'hub-admin-modal-overlay';
    this.overlay.addEventListener('click', (e) => {
      if (e.target === this.overlay) this.close();
    });

    this.dialog = document.createElement('div');
    this.dialog.className = 'hub-admin-modal';
    if (opts.width) this.dialog.style.width = opts.width;

    // Header
    const header = document.createElement('div');
    header.className = 'hub-admin-modal-header';
    const titleEl = document.createElement('span');
    titleEl.textContent = opts.title;
    header.appendChild(titleEl);
    const closeBtn = document.createElement('button');
    closeBtn.className = 'hub-admin-icon-btn';
    closeBtn.innerHTML = ICON.x;
    closeBtn.addEventListener('click', () => this.close());
    header.appendChild(closeBtn);
    this.dialog.appendChild(header);

    // Body
    this.body = document.createElement('div');
    this.body.className = 'hub-admin-modal-body';
    this.dialog.appendChild(this.body);

    // Footer
    this.footer = document.createElement('div');
    this.footer.className = 'hub-admin-modal-footer';
    this.dialog.appendChild(this.footer);

    this.overlay.appendChild(this.dialog);
  }

  addAction(label: string, cls: string, handler: () => void): HTMLButtonElement {
    const btn = document.createElement('button');
    btn.className = cls;
    btn.textContent = label;
    btn.addEventListener('click', handler);
    this.footer.appendChild(btn);
    return btn;
  }

  open(): void {
    document.body.appendChild(this.overlay);
  }

  close(): void {
    this.overlay.remove();
  }
}
