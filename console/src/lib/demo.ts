/**
 * Demo mode — renders seeded mock data and bypasses OIDC so the console is
 * fully interactive without a live hub. Enabled by `?demo=1` (persisted) or
 * `localStorage['hub-console-demo']='1'`. Default off (real backend + auth).
 */
const KEY = 'hub-console-demo'

let resolved: boolean | null = null

export function isDemoMode(): boolean {
  if (resolved !== null) return resolved
  try {
    const params = new URLSearchParams(window.location.search)
    if (params.get('demo') === '1') localStorage.setItem(KEY, '1')
    if (params.get('demo') === '0') localStorage.removeItem(KEY)
    resolved = localStorage.getItem(KEY) === '1'
  } catch {
    resolved = false
  }
  return resolved
}

/** Resolve to mock data in demo mode, otherwise run the real fetcher. */
export async function withDemo<T>(mock: T | (() => T), real: () => Promise<T>): Promise<T> {
  if (isDemoMode()) {
    const value = typeof mock === 'function' ? (mock as () => T)() : mock
    // Small delay so loading states are exercised in demo mode.
    return new Promise((r) => setTimeout(() => r(value), 120))
  }
  return real()
}
