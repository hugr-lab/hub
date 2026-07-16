import { apiUrl } from './config'
import { authHeader, reportUnauthorized } from './auth-token'

export interface GraphQLError {
  message: string
  path?: (string | number)[]
  extensions?: Record<string, unknown>
}

export class GraphQLRequestError extends Error {
  errors: GraphQLError[]
  constructor(errors: GraphQLError[]) {
    super(errors.map((e) => e.message).join('; ') || 'GraphQL error')
    this.name = 'GraphQLRequestError'
    this.errors = errors
  }
}

/** A non-2xx response from the proxy/hugr (e.g. 403 on a denied impersonation). */
export class GraphQLHTTPError extends Error {
  status: number
  constructor(status: number) {
    super(`GraphQL HTTP ${status}`)
    this.name = 'GraphQLHTTPError'
    this.status = status
  }
}

/**
 * POST a GraphQL operation to the hub's `/hugr` proxy. The user's OIDC bearer
 * is attached; the query-engine forwards it verbatim. Throws on transport
 * failure or any `errors[]` in the response (hugr sends a plural errors part).
 *
 * `extraHeaders` attaches per-call headers — e.g. `X-Hugr-Impersonated-Role` for
 * the admin role-access preview (the hub proxy forwards it; hugr gates it on the
 * caller's own role carrying `can_impersonate`).
 */
export async function postGraphQL<T = unknown>(
  query: string,
  variables?: Record<string, unknown>,
  extraHeaders?: Record<string, string>,
): Promise<T> {
  const res = await fetch(apiUrl('/hugr'), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(await authHeader()),
      ...(extraHeaders ?? {}),
    },
    body: JSON.stringify({ query, variables: variables ?? {} }),
  })

  if (res.status === 401) {
    reportUnauthorized()
    throw new Error('Unauthorized')
  }
  if (!res.ok) {
    throw new GraphQLHTTPError(res.status)
  }

  const body = (await res.json()) as { data?: T; errors?: GraphQLError[] }
  if (body.errors && body.errors.length > 0) {
    throw new GraphQLRequestError(body.errors)
  }
  return body.data as T
}
