import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Page, ApiHint } from '@/components/shell/Page'
import {
  Badge,
  Banner,
  Button,
  EmptyState,
  Field,
  Modal,
  Segmented,
  SearchField,
  Spinner,
  useToast,
} from '@/components/ui'
import { cn } from '@/lib/cn'
import { useSession } from '@/lib/session'
import {
  downloadBundle,
  grantKey,
  grantSkillCapability,
  listCapabilityGrants,
  listSkills,
  publishBundle,
  revokeSkillCapability,
  setSkillPublish,
  type CapabilityGrants,
  type CatalogSkill,
} from '@/api/skills'

type SkillTab = 'catalog' | 'grants'

const PAGE_SIZE = 6

function shortHash(hash: string): string {
  return hash ? `sha256:${hash.slice(0, 12)}…` : '—'
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

export function SkillsScreen() {
  // Admin marketplace tab only on the real admin surface (not /app or view-as-owner).
  const { effectiveAdmin: isAdmin } = useSession()
  const { success, error: toastError } = useToast()
  const qc = useQueryClient()

  const [tab, setTab] = useState<SkillTab>('catalog')
  const effectiveTab: SkillTab = isAdmin ? tab : 'catalog'

  return (
    <Page>
      <div className="flex items-center gap-2.5">
        {isAdmin && (
          <Segmented<SkillTab>
            value={effectiveTab}
            onChange={setTab}
            options={[
              { value: 'catalog', label: 'Catalog' },
              { value: 'grants', label: 'Capability grants' },
            ]}
          />
        )}
        <span className="flex-1" />
        <PublishButton
          onPublished={() => qc.invalidateQueries({ queryKey: ['skills', 'catalog'] })}
          success={success}
        />
      </div>

      {effectiveTab === 'catalog' ? (
        <CatalogTab success={success} toastError={toastError} />
      ) : (
        <GrantsTab success={success} toastError={toastError} />
      )}
    </Page>
  )
}

/* ── Publish (button + modal) ─────────────────────────────────────────── */

function PublishButton({
  onPublished,
  success,
}: {
  onPublished: () => void
  success: (m: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [file, setFile] = useState<File | null>(null)
  const [validationError, setValidationError] = useState<string | null>(null)

  const publishMut = useMutation({
    mutationFn: (f: File) => publishBundle(f),
    onSuccess: (r) => {
      success(`POST /skills/publish → ${r.name} v${r.version} · ${r.status}`)
      onPublished()
      close(false)
    },
    onError: (e: unknown) => setValidationError(e instanceof Error ? e.message : String(e)),
  })

  function close(next: boolean) {
    setOpen(next)
    if (!next) {
      setFile(null)
      setValidationError(null)
    }
  }

  return (
    <>
      <Button variant="primary" onClick={() => setOpen(true)}>
        ↑ Publish bundle…
      </Button>
      <Modal
        open={open}
        onOpenChange={close}
        title="Publish skill bundle"
        description="Upload a .tar.gz bundle with a SKILL.md manifest at the root."
        width={460}
        footer={
          <>
            <Button variant="ghost" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={!file || publishMut.isPending}
              onClick={() => file && publishMut.mutate(file)}
            >
              {publishMut.isPending ? 'Publishing…' : '↑ Publish'}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          {validationError && <Banner tone="error">{validationError}</Banner>}
          <Field label="Bundle archive" hint="tar.gz — server validates SKILL.md, name, and declared capabilities.">
            <input
              type="file"
              accept=".tar.gz,.tgz,.gz,application/gzip"
              onChange={(e) => {
                setFile(e.target.files?.[0] ?? null)
                setValidationError(null)
              }}
              className={cn(
                'text-xs text-text2',
                'file:mr-3 file:rounded-btn file:border file:border-border file:bg-surface file:px-2.5 file:py-1.5',
                'file:text-xs file:font-medium file:text-text hover:file:bg-surface2',
              )}
            />
          </Field>
          {file && (
            <div className="rounded-btn bg-surface2 px-3 py-2 text-xs text-text2">
              <span className="font-mono">{file.name}</span> · {formatBytes(file.size)}
            </div>
          )}
          <ApiHint>POST /skills/publish · gate: hugen:skill.publish</ApiHint>
        </div>
      </Modal>
    </>
  )
}

/* ── Catalog tab ──────────────────────────────────────────────────────── */

function CatalogTab({
  success,
  toastError,
}: {
  success: (m: string) => void
  toastError: (m: string) => void
}) {
  const [query, setQuery] = useState('')
  const [shown, setShown] = useState(PAGE_SIZE)

  const catalogQ = useQuery({ queryKey: ['skills', 'catalog'], queryFn: listSkills })

  const downloadMut = useMutation({
    mutationFn: (sk: CatalogSkill) => downloadBundle(sk.name, sk.version),
    onSuccess: (r, sk) => success(`GET /skills/${sk.name}/bundle → ${r.filename}`),
    onError: (e: unknown) => toastError(e instanceof Error ? e.message : String(e)),
  })

  const skills = catalogQ.data ?? []
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return skills
    return skills.filter(
      (sk) =>
        sk.name.toLowerCase().includes(q) ||
        sk.description.toLowerCase().includes(q) ||
        sk.capabilities.some((c) => c.toLowerCase().includes(q)),
    )
  }, [skills, query])
  const visible = filtered.slice(0, shown)

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2.5">
        <SearchField
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setShown(PAGE_SIZE)
          }}
          placeholder="Search skills…"
          className="max-w-[340px] flex-1"
        />
        <span className="text-xs text-text3">
          {filtered.length} {filtered.length === 1 ? 'skill' : 'skills'}
        </span>
      </div>

      {catalogQ.isLoading ? (
        <div className="flex items-center gap-2 py-6 text-sm text-text3">
          <Spinner /> Loading catalog…
        </div>
      ) : catalogQ.isError ? (
        <Banner tone="error">Could not load the skills catalog.</Banner>
      ) : filtered.length === 0 ? (
        <EmptyState
          title={query ? 'No skills match your search' : 'No skills available'}
          description={
            query
              ? 'Try a different name or capability.'
              : 'The catalog is empty, or your role lacks the capabilities these skills require.'
          }
        />
      ) : (
        <>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-3">
            {visible.map((sk) => {
              const downloading =
                downloadMut.isPending && downloadMut.variables?.name === sk.name
              return (
                <div
                  key={sk.name}
                  className="flex flex-col gap-2.5 rounded-card border border-border bg-surface p-4 shadow-card"
                >
                  <div className="flex items-baseline gap-2">
                    <span className="font-mono text-sm font-bold">{sk.name}</span>
                    <span className="font-mono text-2xs text-text3">v{sk.version}</span>
                    <span className="flex-1" />
                    <span className="truncate text-2xs text-text3">by {sk.publisher}</span>
                  </div>
                  <p className="flex-1 text-xs leading-relaxed text-text2">{sk.description}</p>
                  <div className="flex flex-wrap gap-1.5">
                    {sk.capabilities.map((c) => (
                      <Badge key={c} tone="accent" mono className="normal-case tracking-normal">
                        {c}
                      </Badge>
                    ))}
                    {sk.capabilities.length === 0 && (
                      <span className="text-2xs text-text3">no required capabilities</span>
                    )}
                  </div>
                  <div className="flex items-center gap-2 border-t border-border pt-2.5">
                    <span
                      className="flex-1 truncate font-mono text-2xs text-text3"
                      title={sk.contentHash ? `sha256:${sk.contentHash}` : undefined}
                    >
                      {shortHash(sk.contentHash)}
                    </span>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={downloading}
                      onClick={() => downloadMut.mutate(sk)}
                    >
                      {downloading ? 'Downloading…' : '↓ Bundle'}
                    </Button>
                  </div>
                </div>
              )
            })}
          </div>
          {filtered.length > shown && (
            <Button
              variant="secondary"
              className="self-center"
              onClick={() => setShown((n) => n + PAGE_SIZE)}
            >
              Load more
            </Button>
          )}
        </>
      )}

      <ApiHint>GET /skills/catalog — server hides skills whose capabilities your role lacks</ApiHint>
    </div>
  )
}

/* ── Capability grants tab (admin) ────────────────────────────────────── */

function GrantsTab({
  success,
  toastError,
}: {
  success: (m: string) => void
  toastError: (m: string) => void
}) {
  const grantsQ = useQuery({ queryKey: ['skillGrants'], queryFn: listCapabilityGrants })

  // The matrix is edited in place (like a spreadsheet): local state is the
  // source of truth once loaded. Toggles fire grant/revoke mutations but do NOT
  // invalidate the read (that would clobber in-flight edits / demo state).
  const [matrix, setMatrix] = useState<CapabilityGrants | null>(null)
  useEffect(() => {
    if (grantsQ.data) setMatrix(grantsQ.data)
  }, [grantsQ.data])

  const [newRole, setNewRole] = useState('')
  const [newCap, setNewCap] = useState('')

  const capMut = useMutation({
    mutationFn: (v: { role: string; cap: string; on: boolean }) =>
      v.on ? revokeSkillCapability(v.role, v.cap) : grantSkillCapability(v.role, v.cap),
    onMutate: (v) => {
      const key = grantKey(v.role, v.cap)
      setMatrix((m) => (m ? { ...m, grants: { ...m.grants, [key]: !v.on } } : m))
      return { key, prev: v.on }
    },
    onSuccess: (_r, v) =>
      success(`${v.on ? 'revoke' : 'grant'}_skill_capability("${v.role}", "${v.cap}")`),
    onError: (e: unknown, _v, ctx) => {
      if (ctx) setMatrix((m) => (m ? { ...m, grants: { ...m.grants, [ctx.key]: ctx.prev } } : m))
      toastError(e instanceof Error ? e.message : String(e))
    },
  })

  const pubMut = useMutation({
    mutationFn: (v: { role: string; on: boolean }) => setSkillPublish(v.role, !v.on),
    onMutate: (v) => {
      setMatrix((m) => (m ? { ...m, publish: { ...m.publish, [v.role]: !v.on } } : m))
      return { role: v.role, prev: v.on }
    },
    onSuccess: (_r, v) => success(`set_skill_publish("${v.role}", ${!v.on})`),
    onError: (e: unknown, _v, ctx) => {
      if (ctx) setMatrix((m) => (m ? { ...m, publish: { ...m.publish, [ctx.role]: ctx.prev } } : m))
      toastError(e instanceof Error ? e.message : String(e))
    },
  })

  function addRole() {
    const v = newRole.trim()
    if (!v) return
    setMatrix((m) => (m && !m.roles.includes(v) ? { ...m, roles: [...m.roles, v] } : m))
    setNewRole('')
  }

  function addCap() {
    const v = newCap.trim()
    if (!v) return
    setMatrix((m) => (m && !m.capabilities.includes(v) ? { ...m, capabilities: [...m.capabilities, v] } : m))
    setNewCap('')
  }

  if (grantsQ.isLoading || !matrix) {
    return (
      <div className="flex items-center gap-2 py-6 text-sm text-text3">
        <Spinner /> Loading grants…
      </div>
    )
  }
  if (grantsQ.isError) {
    return <Banner tone="error">Could not load capability grants.</Banner>
  }

  const caps = matrix.capabilities
  const gridCols = `150px repeat(${caps.length}, minmax(0, 1fr)) 90px`
  const minWidth = 240 + caps.length * 96

  return (
    <div className="flex flex-col gap-3">
      <div className="overflow-x-auto rounded-card border border-border bg-surface">
        <div style={{ minWidth }}>
          {/* header */}
          <div
            className="grid border-b border-border"
            style={{ gridTemplateColumns: gridCols }}
          >
            <span className="eyebrow px-4 py-2.5">Role</span>
            {caps.map((c) => (
              <span
                key={c}
                className="border-l border-border px-2 py-2.5 text-center font-mono text-2xs font-semibold text-text2"
                title={c}
              >
                {c}
              </span>
            ))}
            <span className="eyebrow border-l border-border px-2 py-2.5 text-center">Publish</span>
          </div>
          {/* rows */}
          {matrix.roles.map((role) => {
            const pub = !!matrix.publish[role]
            return (
              <div
                key={role}
                className="grid border-b border-border last:border-b-0"
                style={{ gridTemplateColumns: gridCols }}
              >
                <span className="truncate px-4 py-2 font-mono text-xs font-semibold" title={role}>
                  {role}
                </span>
                {caps.map((c) => {
                  const on = !!matrix.grants[grantKey(role, c)]
                  return (
                    <button
                      key={c}
                      onClick={() => capMut.mutate({ role, cap: c, on })}
                      title={`${on ? 'revoke' : 'grant'}_skill_capability("${role}", "${c}")`}
                      className={cn(
                        'flex items-center justify-center border-l border-border py-2 text-xs font-bold transition hover:brightness-95',
                        on ? 'bg-accent-soft text-accent' : 'text-text3 hover:bg-surface2',
                      )}
                    >
                      {on ? '✓' : '·'}
                    </button>
                  )
                })}
                <button
                  onClick={() => pubMut.mutate({ role, on: pub })}
                  title={`set_skill_publish("${role}", ${!pub})`}
                  className={cn(
                    'flex items-center justify-center border-l border-border py-2 text-xs font-bold transition hover:brightness-95',
                    pub ? 'bg-green-soft text-green' : 'text-text3 hover:bg-surface2',
                  )}
                >
                  {pub ? '✓' : '·'}
                </button>
              </div>
            )
          })}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          value={newRole}
          onChange={(e) => setNewRole(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && addRole()}
          placeholder="add role…"
          className="w-40 rounded-btn border border-border2 bg-surface px-2.5 py-1.5 font-mono text-xs text-text placeholder:text-text3 focus:outline focus:outline-2 focus:outline-accent"
        />
        <Button variant="secondary" size="sm" onClick={addRole}>
          ＋ Role
        </Button>
        <span className="w-2" />
        <input
          value={newCap}
          onChange={(e) => setNewCap(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && addCap()}
          placeholder="add capability…"
          className="w-40 rounded-btn border border-border2 bg-surface px-2.5 py-1.5 font-mono text-xs text-text placeholder:text-text3 focus:outline focus:outline-2 focus:outline-accent"
        />
        <Button variant="secondary" size="sm" onClick={addCap}>
          ＋ Capability
        </Button>
      </div>

      <ApiHint>
        grant_skill_capability / revoke_skill_capability · set_skill_publish(role, enabled)
      </ApiHint>
    </div>
  )
}
