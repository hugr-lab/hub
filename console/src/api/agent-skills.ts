/**
 * Per-agent skills — hub-native endpoints (`/api/v1/agents/{id}/skills…`) that
 * the hub authorizes then forwards to the agent's hugen `/v1/skills` API. List
 * is member+; export + install require owner or admin (enforced hub-side). The
 * marketplace publish posts a bundle to the hub `/skills/publish` surface
 * (gated on the hugr `hugen:skill.publish` capability).
 */
import { restJSON, restRaw } from '@/lib/rest'

export interface AgentSkill {
  name: string
  description: string
  /** system | hub | local | dynamic | inline */
  origin: string
  /** effective tier compatibility (root/mission/worker) */
  tiers: string[]
  task_eligible: boolean
  keywords?: string[]
  /** has a bundle FS → can be exported (inline skills can't) */
  exportable: boolean
  /** local/dynamic → can be overwritten / uninstalled */
  writable: boolean
}

const agentBase = (id: string) => `/api/v1/agents/${encodeURIComponent(id)}/skills`

/** List an agent's installed skills, grouped client-side by origin. */
export async function listAgentSkills(agentId: string): Promise<AgentSkill[]> {
  const res = await restJSON<{ skills: AgentSkill[] }>(agentBase(agentId))
  return res.skills ?? []
}

/** Download a skill bundle (tar.gz) as a Blob. */
export async function exportAgentSkill(agentId: string, name: string): Promise<Blob> {
  const res = await restRaw(`${agentBase(agentId)}/${encodeURIComponent(name)}/export`)
  if (!res.ok) throw await proxyError(res, 'Export failed')
  return res.blob()
}

/** Install an uploaded bundle (tar.gz) as a local skill on the agent. */
export async function installAgentSkill(agentId: string, bundle: Blob, overwrite = false): Promise<void> {
  const res = await restRaw(`${agentBase(agentId)}/install${overwrite ? '?overwrite=true' : ''}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/gzip' },
    body: bundle,
  })
  if (!res.ok) throw await proxyError(res, 'Install failed')
}

/**
 * Publish a skill to the hub marketplace: export its bundle from the agent, then
 * POST the tar.gz to `/skills/publish`. Admin-only in practice (the hub gates on
 * the hugr `hugen:skill.publish` capability).
 */
export async function publishAgentSkillToMarketplace(agentId: string, name: string): Promise<void> {
  const bundle = await exportAgentSkill(agentId, name)
  const res = await restRaw('/skills/publish', {
    method: 'POST',
    headers: { 'Content-Type': 'application/gzip' },
    body: bundle,
  })
  if (!res.ok) throw await proxyError(res, 'Publish failed')
}

/** Extract a human message from a hub gatewayError ({error:{message}}) or a
 *  forwarded hugen error ({error:"…"}). */
async function proxyError(res: Response, fallback: string): Promise<Error> {
  const text = await res.text().catch(() => '')
  let msg = fallback
  try {
    const j = JSON.parse(text) as { error?: unknown; message?: string }
    const e = j.error
    if (typeof e === 'string') msg = e
    else if (e && typeof e === 'object' && 'message' in e && typeof (e as { message?: string }).message === 'string') {
      msg = (e as { message: string }).message
    } else if (typeof j.message === 'string') {
      msg = j.message
    }
  } catch {
    /* non-JSON body */
  }
  return new Error(`${msg} (HTTP ${res.status})`)
}
