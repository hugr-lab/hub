export interface RuntimeConfig {
  oidc_issuer: string
  oidc_client_id: string
  oidc_scopes: string
  /** API base; empty means same origin as the console. */
  api_base: string
}

let cached: RuntimeConfig | null = null

/**
 * Load runtime config from the hub-served `/console/config.json`. Values vary
 * per deployment (issuer/client), so they are never baked into the bundle.
 */
export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  if (cached) return cached
  const res = await fetch(`${import.meta.env.BASE_URL}config.json`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`config.json ${res.status}`)
  const raw = (await res.json()) as Partial<RuntimeConfig>
  cached = {
    oidc_issuer: raw.oidc_issuer ?? '',
    oidc_client_id: raw.oidc_client_id ?? 'hugr',
    oidc_scopes: raw.oidc_scopes ?? 'openid profile email',
    api_base: raw.api_base ?? '',
  }
  return cached
}

/** Synchronous access to already-loaded config (throws if not loaded yet). */
export function runtimeConfig(): RuntimeConfig {
  if (!cached) throw new Error('runtime config not loaded')
  return cached
}

/** Resolve an API path against the configured base. */
export function apiUrl(path: string): string {
  const base = cached?.api_base ?? ''
  return `${base}${path}`
}
