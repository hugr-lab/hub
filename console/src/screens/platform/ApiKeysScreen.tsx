import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, X } from 'lucide-react'
import { Page, PageHeader, ApiHint } from '@/components/shell/Page'
import {
  Badge,
  Banner,
  Button,
  DataTable,
  Field,
  Input,
  Modal,
  Select,
  Textarea,
  Toggle,
  useToast,
  type Column,
  type Tone,
} from '@/components/ui'
import {
  deleteApiKey,
  insertApiKey,
  listApiKeys,
  setApiKeyDisabled,
  type ApiKey,
  type NewApiKey,
} from '@/api/platform-keys'
import { listRoles } from '@/api/platform-roles'

const EMPTY_FORM: NewApiKey = {
  name: '',
  description: '',
  default_role: 'viewer',
  is_temporal: false,
  expires_at: null,
  headers: '',
  claims: '',
}

function jsonError(text: string): string | null {
  const trimmed = text.trim()
  if (!trimmed) return null
  try {
    JSON.parse(trimmed)
    return null
  } catch {
    return 'Not valid JSON'
  }
}

function statusOf(k: ApiKey): { tone: Tone; label: string } {
  if (k.disabled) return { tone: 'red', label: 'disabled' }
  if (k.is_temporal) return { tone: 'amber', label: 'temporal' }
  return { tone: 'green', label: 'active' }
}

export function ApiKeysScreen() {
  const qc = useQueryClient()
  const toast = useToast()

  const keys = useQuery({ queryKey: ['apiKeys'], queryFn: listApiKeys })
  const roles = useQuery({ queryKey: ['roles'], queryFn: listRoles })

  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<NewApiKey>(EMPTY_FORM)
  const [revealed, setRevealed] = useState<string | null>(null)

  const invalidate = () => qc.invalidateQueries({ queryKey: ['apiKeys'] })

  const createMut = useMutation({
    mutationFn: (input: NewApiKey) => insertApiKey(input),
    onSuccess: ({ key }, input) => {
      setRevealed(key)
      setOpen(false)
      setForm(EMPTY_FORM)
      invalidate()
      toast.success(`insert_api_keys(data:{name:"${input.name}"}) → created`)
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : 'insert_api_keys failed'),
  })

  const toggleMut = useMutation({
    mutationFn: (k: ApiKey) => setApiKeyDisabled(k.name, !k.disabled),
    onSuccess: (_r, k) => {
      invalidate()
      toast.success(`update_api_keys("${k.name}", disabled: ${!k.disabled})`)
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : 'update_api_keys failed'),
  })

  const deleteMut = useMutation({
    mutationFn: (name: string) => deleteApiKey(name),
    onSuccess: (_r, name) => {
      invalidate()
      toast.success(`delete_api_keys(filter:{name:{eq:"${name}"}})`)
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : 'delete_api_keys failed'),
  })

  const headersErr = jsonError(form.headers)
  const claimsErr = jsonError(form.claims)
  const canCreate = form.name.trim().length > 0 && !headersErr && !claimsErr

  const copyKey = async () => {
    if (!revealed) return
    try {
      await navigator.clipboard.writeText(revealed)
      toast.success('API key copied to clipboard')
    } catch {
      toast.error('Clipboard unavailable — select and copy manually')
    }
  }

  const columns: Column<ApiKey>[] = [
    {
      key: 'name',
      header: 'Name',
      width: '1.1fr',
      cell: (k) => <span className="truncate font-mono text-xs font-semibold">{k.name}</span>,
    },
    {
      key: 'role',
      header: 'Role',
      width: '1fr',
      cell: (k) => <span className="truncate font-mono text-xs text-text2">{k.default_role}</span>,
    },
    {
      key: 'expiry',
      header: 'Expiry',
      width: '0.8fr',
      cell: (k) => <span className="font-mono text-xs text-text2">{k.expires_at ?? '—'}</span>,
    },
    {
      key: 'status',
      header: 'Status',
      width: '0.8fr',
      cell: (k) => {
        const s = statusOf(k)
        return <Badge tone={s.tone}>{s.label}</Badge>
      },
    },
    {
      key: 'description',
      header: 'Description',
      width: 'minmax(0,1.2fr)',
      cell: (k) => <span className="truncate text-xs text-text3">{k.description || '—'}</span>,
    },
    {
      key: 'actions',
      header: 'Actions',
      width: '150px',
      align: 'right',
      cell: (k) => (
        <div className="flex items-center justify-end gap-1.5">
          <Button
            size="sm"
            variant="secondary"
            disabled={toggleMut.isPending}
            onClick={() => toggleMut.mutate(k)}
          >
            {k.disabled ? 'Enable' : 'Disable'}
          </Button>
          <Button
            size="sm"
            variant="danger-ghost"
            disabled={deleteMut.isPending}
            onClick={() => {
              if (window.confirm(`Delete API key "${k.name}"? This cannot be undone.`)) {
                deleteMut.mutate(k.name)
              }
            }}
          >
            Delete
          </Button>
        </div>
      ),
    },
  ]

  return (
    <Page>
      <PageHeader
        title="API Keys"
        subtitle="Service-to-service auth — each key carries a default_role."
        actions={
          <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
            ＋ Create key
          </Button>
        }
      />

      {revealed && (
        <Banner tone="reveal" className="flex items-center gap-3">
          <span className="text-xs font-semibold text-green">
            Key created — copy it now, it won't be shown again:
          </span>
          <span className="min-w-0 flex-1 break-all font-mono text-xs">{revealed}</span>
          <Button size="sm" variant="secondary" onClick={copyKey}>
            <Copy className="h-3.5 w-3.5" /> Copy
          </Button>
          <button
            aria-label="Dismiss"
            className="rounded-[6px] p-1 text-text3 hover:bg-surface2 hover:text-text"
            onClick={() => setRevealed(null)}
          >
            <X className="h-4 w-4" />
          </button>
        </Banner>
      )}

      <DataTable
        columns={columns}
        rows={keys.data ?? []}
        getKey={(k) => k.name}
        empty={
          <div className="py-6 text-center text-sm text-text3">
            {keys.isLoading ? 'Loading keys…' : 'No API keys yet. Create one to authenticate a service.'}
          </div>
        }
      />

      <ApiHint>core.api_keys · service-to-service auth with a default_role</ApiHint>

      <Modal
        open={open}
        onOpenChange={setOpen}
        title="Create API key"
        width={460}
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={!canCreate || createMut.isPending}
              onClick={() => createMut.mutate(form)}
            >
              insert_api_keys →
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <Field label="name">
            <Input
              mono
              autoFocus
              placeholder="e.g. superset-reader"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label="default_role">
              <Select
                className="font-mono"
                value={form.default_role}
                onChange={(e) => setForm((f) => ({ ...f, default_role: e.target.value }))}
              >
                {(roles.data ?? []).map((r) => (
                  <option key={r.name} value={r.name}>
                    {r.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="expires_at (optional)" hint="ISO date — leave blank for a permanent key">
              <Input
                mono
                placeholder="2026-12-31"
                value={form.expires_at ?? ''}
                onChange={(e) =>
                  setForm((f) => ({ ...f, expires_at: e.target.value.trim() ? e.target.value : null }))
                }
              />
            </Field>
          </div>

          <label className="flex items-center gap-2 text-xs font-medium text-text2">
            <Toggle
              checked={form.is_temporal}
              onCheckedChange={(v) => setForm((f) => ({ ...f, is_temporal: v }))}
            />
            is_temporal — rotate / expire this key
          </label>

          <div className="grid grid-cols-2 gap-3">
            <Field label="headers (JSON)" hint={headersErr ?? undefined}>
              <Textarea
                mono
                rows={3}
                spellCheck={false}
                placeholder={'{ "X-Tenant": "acme" }'}
                value={form.headers}
                onChange={(e) => setForm((f) => ({ ...f, headers: e.target.value }))}
                className={headersErr ? 'border-red' : undefined}
              />
            </Field>
            <Field label="claims (JSON)" hint={claimsErr ?? undefined}>
              <Textarea
                mono
                rows={3}
                spellCheck={false}
                placeholder={'{ "team": "bi" }'}
                value={form.claims}
                onChange={(e) => setForm((f) => ({ ...f, claims: e.target.value }))}
                className={claimsErr ? 'border-red' : undefined}
              />
            </Field>
          </div>

          <Field label="description">
            <Input
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
            />
          </Field>
        </div>
      </Modal>
    </Page>
  )
}
