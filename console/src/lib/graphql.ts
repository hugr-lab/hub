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

/**
 * POST a GraphQL operation to the hub's `/hugr` proxy. The user's OIDC bearer
 * is attached; the query-engine forwards it verbatim. Throws on transport
 * failure or any `errors[]` in the response (hugr sends a plural errors part).
 */
export async function postGraphQL<T = unknown>(
  query: string,
  variables?: Record<string, unknown>,
): Promise<T> {
  const res = await fetch(apiUrl('/hugr'), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(await authHeader()),
    },
    body: JSON.stringify({ query, variables: variables ?? {} }),
  })

  if (res.status === 401) {
    reportUnauthorized()
    throw new Error('Unauthorized')
  }
  if (!res.ok) {
    throw new Error(`GraphQL HTTP ${res.status}`)
  }

  const body = (await res.json()) as { data?: T; errors?: GraphQLError[] }
  if (body.errors && body.errors.length > 0) {
    throw new GraphQLRequestError(body.errors)
  }
  return body.data as T
}
