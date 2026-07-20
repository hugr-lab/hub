/**
 * Per-agent MCP tool providers — hub-native endpoints that forward to / persist
 * for the agent's runtime. List is member+; add/remove are owner/admin (enforced
 * hub-side). Providers are remote HTTP/SSE MCP servers, always per_agent, so a
 * change goes live in every session at once.
 */
import { restJSON, RestError } from '@/lib/rest'

export interface ToolProvider {
  name: string
  transport: string
  endpoint?: string
  auth?: string
  /** currently registered on the running agent's root ToolManager */
  live: boolean
}

export interface ToolProviderInput {
  name: string
  transport: string
  endpoint: string
  auth?: string
  headers?: Record<string, string>
}

const base = (id: string) => `/api/v1/agents/${encodeURIComponent(id)}/tool-providers`

/** List the agent's managed remote-MCP providers. */
export async function listToolProviders(agentId: string): Promise<ToolProvider[]> {
  return (await restJSON<ToolProvider[]>(base(agentId))) ?? []
}

/** Add or update a remote MCP provider (upsert by name). */
export async function upsertToolProvider(agentId: string, input: ToolProviderInput): Promise<void> {
  await restJSON(base(agentId), { method: 'POST', json: input })
}

/** Remove a remote MCP provider by name. */
export async function deleteToolProvider(agentId: string, name: string): Promise<void> {
  await restJSON(`${base(agentId)}/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

/** Extract a human message from a RestError raised by the calls above. */
export function providerErrText(e: unknown): string {
  if (e instanceof RestError) {
    try {
      const j = JSON.parse(e.body ?? '') as { error?: { message?: string } | string }
      const err = j.error
      if (typeof err === 'string') return err
      if (err && typeof err === 'object' && err.message) return err.message
    } catch {
      /* non-JSON body */
    }
    return `HTTP ${e.status}`
  }
  return e instanceof Error ? e.message : String(e)
}
