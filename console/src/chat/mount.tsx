import { createRoot, type Root } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ChatApp, type ChatAppProps } from './ChatApp'
import './chat.css'

export type { ChatAppProps } from './ChatApp'
export { ChatApp } from './ChatApp'

/**
 * Framework-agnostic entry point. Mounts the chat into any host element (the
 * SPA, or a JupyterLab Lumino `Widget` calling `mountChat(this.node, props)` in
 * `onAfterAttach` and `unmount()` in `dispose`).
 */
export function mountChat(element: HTMLElement, props: ChatAppProps): { unmount(): void } {
  const qc = new QueryClient({
    defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } },
  })
  if (props.theme) element.setAttribute('data-theme', props.theme)
  const root: Root = createRoot(element)
  root.render(
    <QueryClientProvider client={qc}>
      <ChatApp {...props} />
    </QueryClientProvider>,
  )
  return {
    unmount() {
      root.unmount()
    },
  }
}

/**
 * `<hub-chat api-base chat-id agent-id theme demo token>` custom element.
 * The token comes from the `token` attribute or a `window.hubChatGetToken`
 * global; hosts that can pass a function should prefer `mountChat` directly.
 */
class HubChatElement extends HTMLElement {
  private handle: { unmount(): void } | null = null

  connectedCallback() {
    const apiBase = this.getAttribute('api-base') ?? ''
    const chatId = this.getAttribute('chat-id')
    const agentId = this.getAttribute('agent-id') ?? undefined
    const theme = (this.getAttribute('theme') as 'light' | 'dark' | null) ?? undefined
    const demo = this.getAttribute('demo') === 'true'
    const tokenAttr = this.getAttribute('token')
    const getToken = () =>
      tokenAttr ??
      (window as unknown as { hubChatGetToken?: () => string | null }).hubChatGetToken?.() ??
      null

    this.handle = mountChat(this, {
      apiBase,
      getToken,
      chatId: chatId ?? undefined,
      agentId,
      theme,
      demo,
    })
  }

  disconnectedCallback() {
    this.handle?.unmount()
    this.handle = null
  }
}

export function registerHubChatElement(): void {
  if (typeof customElements !== 'undefined' && !customElements.get('hub-chat')) {
    customElements.define('hub-chat', HubChatElement)
  }
}

registerHubChatElement()
