import { postGraphQL } from '@/lib/graphql'
import { restJSON, restRaw, RestError } from '@/lib/rest'
import { withDemo } from '@/lib/demo'

/* ─────────────────────────────────────────────────────────────────────────
 * Skills marketplace API — REST `/skills/*` (bytes) + hub GraphQL fns (grants).
 *
 * Backing handlers (hub `pkg/hubapp/skills_*.go`):
 *   GET  /skills/catalog        → { skills: catalogEntry[] }  (caps-filtered)
 *   GET  /skills/{name}/bundle  → tar.gz  (X-Skill-Version / X-Skill-Content-Hash)
 *   POST /skills/publish        → 201 { name, version, content_hash, status }
 *   function.hub.grant_skill_capability(hugr_role, capability)
 *   function.hub.revoke_skill_capability(hugr_role, capability)
 *   function.hub.set_skill_publish(hugr_role, enabled)
 * ──────────────────────────────────────────────────────────────────────── */

/** One marketplace listing (server hides skills whose caps the caller lacks). */
export interface CatalogSkill {
  name: string
  version: string
  description: string
  /** Best-effort attribution — the catalog exposes `source`, not a publisher. */
  publisher: string
  /** Bare sha256 hex (no algo prefix); render as `sha256:…`. */
  contentHash: string
  /** metadata.hugen.required_capabilities. */
  capabilities: string[]
  source?: string
  taskEligible?: boolean
}

/** Wire shape of one `/skills/catalog` entry (hub `catalogEntry`). */
interface CatalogEntryWire {
  name: string
  version: string
  description?: string
  content_hash?: string
  source?: string
  task_eligible?: boolean
  keywords?: string[]
  tier_compat?: string[]
  required_capabilities?: string[]
}

function stripHashPrefix(h: string | undefined): string {
  if (!h) return ''
  return h.replace(/^sha-?256:/i, '')
}

const MOCK_SKILLS: CatalogSkill[] = [
  {
    name: 'sql-analyst',
    version: '1.4.2',
    publisher: 'hugr-lab',
    description:
      'Schema-aware GraphQL/SQL analysis: introspection, query planning, aggregation chains.',
    capabilities: ['hugr:query'],
    contentHash: '9f2c4b7a1e5d3f80c6a29b4e7d1f0a3c8b5e6d2f4a7c9018e3b6d5f2a1c4e7b90',
    taskEligible: true,
  },
  {
    name: 'geo-tools',
    version: '0.9.0',
    publisher: 'geo-team',
    description:
      'Spatial joins, H3 clustering and buffer analysis over PostGIS and GeoParquet sources.',
    capabilities: ['hugr:query', 'geo:spatial'],
    contentHash: '3a7d1f9c0b8e2d46f5a9c3b7e1d0f8a24c6b5e9d3f7a1c0b8e2d64f5a9c3b7e10',
  },
  {
    name: 'report-writer',
    version: '2.1.0',
    publisher: 'hugr-lab',
    description: 'Compose CSV/Markdown/HTML reports and publish them as chat artifacts.',
    capabilities: ['artifact:write'],
    contentHash: 'c1e8b4d70a2f6935e8c1b4d70a2f6935e8c1b4d70a2f6935e8c1b4d70a2f6935',
    taskEligible: true,
  },
  {
    name: 'web-research',
    version: '0.5.3',
    publisher: 'platform',
    description:
      'Fetch and summarize external HTTP sources. Network egress gated by capability.',
    capabilities: ['net:fetch'],
    contentHash: '7b0d3f6a9c2e5810d4f7b0a3c6e9d2f5817b0d3f6a9c2e58104f7b0a3c6e9d2f5',
  },
  {
    name: 'python-runner',
    version: '1.3.0',
    publisher: 'hugr-lab',
    description:
      'Execute sandboxed Python for dataframe wrangling, plotting and numeric analysis.',
    capabilities: ['python:run', 'hugr:query'],
    contentHash: 'd4a1c7e3b0f68259d4a1c7e3b0f68259d4a1c7e3b0f68259d4a1c7e3b0f68259d',
    taskEligible: true,
  },
  {
    name: 'pipeline-ops',
    version: '1.0.1',
    publisher: 'data-eng',
    description:
      'Trigger reindex, checkpoint and load/unload lifecycle actions on data sources.',
    capabilities: ['hub:lifecycle', 'hugr:query'],
    contentHash: '2f9c6b3e0d7a14852f9c6b3e0d7a14852f9c6b3e0d7a14852f9c6b3e0d7a14852',
  },
  {
    name: 'skill-publisher',
    version: '0.4.0',
    publisher: 'platform',
    description:
      'Package a local skill directory into a bundle and publish it to the shared catalog.',
    capabilities: ['skills:publish'],
    contentHash: '5e2b8d1f4a0c73965e2b8d1f4a0c73965e2b8d1f4a0c73965e2b8d1f4a0c7396',
  },
  {
    name: 'data-quality',
    version: '0.8.4',
    publisher: 'data-eng',
    description: 'Null-rate, drift and constraint checks with a written findings report.',
    capabilities: ['hugr:query', 'artifact:write'],
    contentHash: '8c3e0a6d2f9b74158c3e0a6d2f9b74158c3e0a6d2f9b74158c3e0a6d2f9b7415',
  },
  {
    name: 'timeseries-forecast',
    version: '1.2.0',
    publisher: 'data-science',
    description: 'Fit and evaluate forecasting models over time-series query results.',
    capabilities: ['python:run', 'hugr:query'],
    contentHash: '1d6a3f9c0e8b52471d6a3f9c0e8b52471d6a3f9c0e8b52471d6a3f9c0e8b5247',
  },
]

/** GET /skills/catalog — the caller-role-filtered shared catalog. */
export async function listSkills(): Promise<CatalogSkill[]> {
  return withDemo(MOCK_SKILLS, async () => {
    const d = await restJSON<{ skills?: CatalogEntryWire[] }>('/skills/catalog')
    return (d.skills ?? []).map((e) => ({
      name: e.name,
      version: e.version,
      description: e.description ?? '',
      publisher: e.source || 'shared',
      contentHash: stripHashPrefix(e.content_hash),
      capabilities: e.required_capabilities ?? [],
      source: e.source,
      taskEligible: e.task_eligible,
    }))
  })
}

/* ── bundle download ──────────────────────────────────────────────────── */

export interface BundleDownload {
  filename: string
  version: string
  contentHash: string
  size: number
}

function triggerBrowserDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Revoke on the next tick so the click has committed.
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function filenameFromDisposition(cd: string | null, fallback: string): string {
  if (!cd) return fallback
  const m = /filename="?([^"]+)"?/.exec(cd)
  return m ? m[1] : fallback
}

/**
 * GET /skills/{name}/bundle → download the tar.gz (capability-gated; a miss is
 * a 404). Reads the blob and triggers a browser download.
 */
export async function downloadBundle(name: string, version?: string): Promise<BundleDownload> {
  return withDemo(
    () => {
      const body = `# ${name} ${version ?? ''}\n# demo bundle — /skills/catalog is mocked in demo mode\n`
      const blob = new Blob([body], { type: 'application/gzip' })
      triggerBrowserDownload(blob, `${name}.tar.gz`)
      return { filename: `${name}.tar.gz`, version: version ?? 'demo', contentHash: 'demo', size: blob.size }
    },
    async () => {
      const res = await restRaw(`/skills/${encodeURIComponent(name)}/bundle`)
      if (!res.ok) {
        const text = await res.text().catch(() => '')
        throw new RestError(res.status, `bundle HTTP ${res.status}`, text)
      }
      const blob = await res.blob()
      const v = res.headers.get('X-Skill-Version') ?? version ?? ''
      const hash = stripHashPrefix(res.headers.get('X-Skill-Content-Hash') ?? undefined)
      const filename = filenameFromDisposition(res.headers.get('Content-Disposition'), `${name}.tar.gz`)
      triggerBrowserDownload(blob, filename)
      return { filename, version: v, contentHash: hash, size: blob.size }
    },
  )
}

/* ── publish ──────────────────────────────────────────────────────────── */

export interface PublishResult {
  name: string
  version: string
  content_hash: string
  status: string
}

function parseSkillError(body: string | undefined): string | undefined {
  if (!body) return undefined
  try {
    const j = JSON.parse(body) as { error?: { message?: string; code?: string } }
    return j.error?.message ?? j.error?.code
  } catch {
    return undefined
  }
}

const GZIP_EXT = /\.(tar\.gz|tgz|gz)$/i

/**
 * POST /skills/publish — upload a bundle (raw tar.gz body). Throws a friendly
 * Error carrying the server's validation message (`{error:{message}}`).
 */
export async function publishBundle(file: File): Promise<PublishResult> {
  return withDemo(
    () => {
      if (!GZIP_EXT.test(file.name)) {
        throw new Error('bundle must be a .tar.gz / .tgz archive')
      }
      const short = Math.abs(hashName(file.name)).toString(16).padStart(8, '0').slice(0, 8)
      return {
        name: file.name.replace(GZIP_EXT, ''),
        version: short,
        content_hash: short.repeat(8).slice(0, 64),
        status: 'published',
      }
    },
    async () => {
      try {
        return await restJSON<PublishResult>('/skills/publish', {
          method: 'POST',
          body: file,
          headers: { 'Content-Type': 'application/gzip' },
        })
      } catch (e) {
        if (e instanceof RestError) {
          throw new Error(parseSkillError(e.body) ?? `Publish failed (HTTP ${e.status})`)
        }
        throw e
      }
    },
  )
}

function hashName(s: string): number {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0
  return h
}

/* ── capability grants matrix (admin) ─────────────────────────────────── */

/** roles × capabilities grant matrix + per-role publish flag. */
export interface CapabilityGrants {
  roles: string[]
  capabilities: string[]
  /** key `${role}|${capability}` → granted. */
  grants: Record<string, boolean>
  /** role → may publish to the shared catalog. */
  publish: Record<string, boolean>
}

/** Result of a grant/revoke/publish mutation (hub `skill_permission`). */
export interface SkillPermission {
  role: string
  permission: string
  enabled: boolean
}

export const grantKey = (role: string, capability: string): string => `${role}|${capability}`

// hugr `core.role_permissions` coordinates the real read reconstructs from.
const CAP_TYPE = 'hugen:skill:capability'
const PUB_TYPE = 'hugen:skill'
const PUB_FIELD = 'publish'

const MOCK_GRANTS: CapabilityGrants = {
  roles: ['admin', 'analyst', 'agent:analytics', 'agent:geo', 'agent:etl', 'service-etl', 'viewer', 'support'],
  capabilities: ['hugr:query', 'geo:spatial', 'artifact:write', 'net:fetch', 'python:run', 'hub:lifecycle', 'skills:publish'],
  grants: {
    [grantKey('admin', 'hugr:query')]: true,
    [grantKey('admin', 'geo:spatial')]: true,
    [grantKey('admin', 'artifact:write')]: true,
    [grantKey('admin', 'net:fetch')]: true,
    [grantKey('admin', 'python:run')]: true,
    [grantKey('admin', 'hub:lifecycle')]: true,
    [grantKey('admin', 'skills:publish')]: true,
    [grantKey('analyst', 'hugr:query')]: true,
    [grantKey('analyst', 'artifact:write')]: true,
    [grantKey('analyst', 'python:run')]: true,
    [grantKey('agent:analytics', 'hugr:query')]: true,
    [grantKey('agent:analytics', 'artifact:write')]: true,
    [grantKey('agent:analytics', 'python:run')]: true,
    [grantKey('agent:geo', 'hugr:query')]: true,
    [grantKey('agent:geo', 'geo:spatial')]: true,
    [grantKey('agent:etl', 'hugr:query')]: true,
    [grantKey('agent:etl', 'hub:lifecycle')]: true,
  },
  publish: {
    admin: true,
    analyst: false,
    'agent:analytics': false,
    'agent:geo': false,
    'agent:etl': false,
    viewer: false,
    support: false,
    'service-etl': false,
  },
}

interface RolePermRow {
  role: string
  type_name: string
  field_name: string
  disabled: boolean
}

/**
 * Best-effort read of the grants matrix from `core.role_permissions`.
 *
 * The authority is hugr permission rows: a positive `(hugen:skill:capability,
 * <cap>)` row grants a capability; a NON-disabled `(hugen:skill, publish)` row
 * enables publishing. There is no dedicated read fn, so we reconstruct the
 * matrix from those rows + `core.roles`.
 *
 * Caveat: the capability universe is derived from capabilities that already
 * have at least one grant (a never-granted cap won't appear until an admin
 * adds it via the "＋ Capability" input).
 */
export async function listCapabilityGrants(): Promise<CapabilityGrants> {
  return withDemo(MOCK_GRANTS, async () => {
    const d = await postGraphQL<{
      core: { roles: { name: string }[]; role_permissions: RolePermRow[] }
    }>(
      `query SkillGrants($f: role_permissions_filter) {
        core {
          roles { name }
          role_permissions(filter: $f) { role type_name field_name disabled }
        }
      }`,
      { f: { type_name: { in: [CAP_TYPE, PUB_TYPE] } } },
    )

    const roleSet = new Set<string>()
    const capSet = new Set<string>()
    const grants: Record<string, boolean> = {}
    const publish: Record<string, boolean> = {}

    for (const r of d.core.roles) roleSet.add(r.name)
    for (const row of d.core.role_permissions) {
      roleSet.add(row.role)
      if (row.type_name === CAP_TYPE) {
        capSet.add(row.field_name)
        grants[grantKey(row.role, row.field_name)] = true
      } else if (row.type_name === PUB_TYPE && row.field_name === PUB_FIELD) {
        publish[row.role] = !row.disabled
      }
    }

    return {
      roles: [...roleSet].sort(),
      capabilities: [...capSet].sort(),
      grants,
      publish,
    }
  })
}

/** function.hub.grant_skill_capability(hugr_role, capability). */
export async function grantSkillCapability(role: string, capability: string): Promise<SkillPermission> {
  return withDemo(
    () => ({ role, permission: `${CAP_TYPE}.${capability}`, enabled: true }),
    async () => {
      const d = await postGraphQL<{ function: { hub: { grant_skill_capability: SkillPermission } } }>(
        `mutation GrantCap($r: String!, $c: String!) {
          function { hub { grant_skill_capability(hugr_role: $r, capability: $c) { role permission enabled } } }
        }`,
        { r: role, c: capability },
      )
      return d.function.hub.grant_skill_capability
    },
  )
}

/** function.hub.revoke_skill_capability(hugr_role, capability). */
export async function revokeSkillCapability(role: string, capability: string): Promise<SkillPermission> {
  return withDemo(
    () => ({ role, permission: `${CAP_TYPE}.${capability}`, enabled: false }),
    async () => {
      const d = await postGraphQL<{ function: { hub: { revoke_skill_capability: SkillPermission } } }>(
        `mutation RevokeCap($r: String!, $c: String!) {
          function { hub { revoke_skill_capability(hugr_role: $r, capability: $c) { role permission enabled } } }
        }`,
        { r: role, c: capability },
      )
      return d.function.hub.revoke_skill_capability
    },
  )
}

/** function.hub.set_skill_publish(hugr_role, enabled). */
export async function setSkillPublish(role: string, enabled: boolean): Promise<SkillPermission> {
  return withDemo(
    () => ({ role, permission: `${PUB_TYPE}.${PUB_FIELD}`, enabled }),
    async () => {
      const d = await postGraphQL<{ function: { hub: { set_skill_publish: SkillPermission } } }>(
        `mutation SetPublish($r: String!, $e: Boolean!) {
          function { hub { set_skill_publish(hugr_role: $r, enabled: $e) { role permission enabled } } }
        }`,
        { r: role, e: enabled },
      )
      return d.function.hub.set_skill_publish
    },
  )
}
