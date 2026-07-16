/**
 * Decouples the data layer from the auth implementation. The auth module
 * registers a token provider; the GraphQL/REST clients read the current bearer
 * through `getAccessToken()` and report auth failures via `onUnauthorized`.
 */

type TokenProvider = () => Promise<string | null> | string | null

let provider: TokenProvider = () => null
let unauthorizedHandler: (() => void) | null = null

export function setTokenProvider(fn: TokenProvider): void {
  provider = fn
}

export async function getAccessToken(): Promise<string | null> {
  return provider()
}

/** Build an Authorization header if a token is available. */
export async function authHeader(): Promise<Record<string, string>> {
  const token = await getAccessToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

export function setUnauthorizedHandler(fn: () => void): void {
  unauthorizedHandler = fn
}

export function reportUnauthorized(): void {
  unauthorizedHandler?.()
}
