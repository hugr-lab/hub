/**
 * oidc-client-ts `metadata` seed served by the hub. The hub points
 * token/userinfo/jwks at its own same-origin OIDC reverse-proxy so those
 * CORS-sensitive XHRs don't need a provider web-origin allowance;
 * authorization/end-session/issuer stay the provider's real URLs.
 */
export interface OidcMetadata {
  issuer: string
  authorization_endpoint: string
  token_endpoint: string
  userinfo_endpoint: string
  jwks_uri: string
  end_session_endpoint?: string
}

export interface RuntimeConfig {
  oidc_issuer: string
  oidc_client_id: string
  oidc_scopes: string
  /** API base; empty means same origin as the console. */
  api_base: string
  /** Hub-seeded OIDC metadata; absent → the SPA talks to the issuer directly. */
  oidc?: OidcMetadata
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
    oidc: raw.oidc,
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
