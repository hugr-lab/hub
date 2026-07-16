import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import {
  Modal,
  Button,
  Input,
  Textarea,
  Select,
  Field,
  Banner,
  useToast,
} from '@/components/ui'
import { cn } from '@/lib/cn'
import { useSession } from '@/lib/session'
import { createAgent, type CreateAgentResult } from '@/api/agents'

/** Existing floored roles offered in step 2. TODO: read from `core.roles`. */
const EXISTING_ROLES = ['agent:analytics', 'agent:geo', 'analyst', 'viewer']

type RoleMode = 'existing' | 'new'

const STEP_COUNT = 4

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export function CreateAgentWizard({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const qc = useQueryClient()
  const { success, error } = useToast()
  const { identity } = useSession()

  const [step, setStep] = useState(1)
  const [name, setName] = useState('')
  const [roleMode, setRoleMode] = useState<RoleMode>('existing')
  const [existingRole, setExistingRole] = useState(EXISTING_ROLES[0])
  const [override, setOverride] = useState('{\n  "skills": []\n}')
  const [result, setResult] = useState<CreateAgentResult | null>(null)
  const [copied, setCopied] = useState(false)

  const reset = () => {
    setStep(1)
    setName('')
    setRoleMode('existing')
    setExistingRole(EXISTING_ROLES[0])
    setOverride('{\n  "skills": []\n}')
    setResult(null)
    setCopied(false)
  }

  const close = () => {
    onOpenChange(false)
    // Defer reset so the closing animation doesn't flash step 1.
    setTimeout(reset, 200)
  }

  const overrideError = (() => {
    if (!override.trim()) return null
    try {
      JSON.parse(override)
      return null
    } catch (e) {
      return errText(e)
    }
  })()

  const create = useMutation({
    mutationFn: () =>
      createAgent({
        name: name.trim(),
        hugr_role: roleMode === 'new' ? '' : existingRole,
        config_override: override,
        owner_user_id: identity.userId,
      }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['agents'] })
      setResult(res)
      setStep(4)
      if (roleMode === 'new') success(`create_agent_role("agent:${name.trim()}") → floored role created`)
      success(`create_agent("${res.agent_id}") → provisioned`)
    },
    onError: (e) => error(errText(e)),
  })

  const step1Valid = name.trim().length > 0
  const canAdvance =
    (step === 1 && step1Valid) ||
    (step === 2) ||
    (step === 3 && !overrideError) ||
    step === 4

  const nextLabel = step === 3 ? 'create_agent →' : step === 4 ? 'Done' : 'Continue'
  const canBack = step > 1 && step < 4

  const next = () => {
    if (!canAdvance || create.isPending) return
    if (step === 3) {
      create.mutate()
    } else if (step === 4) {
      close()
    } else {
      setStep((s) => s + 1)
    }
  }

  const copySecret = async () => {
    if (!result) return
    try {
      await navigator.clipboard.writeText(result.secret)
      setCopied(true)
      success('Bootstrap secret copied')
    } catch (e) {
      error(errText(e))
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => (o ? onOpenChange(true) : close())}
      width={480}
      title={
        <span className="flex items-center gap-2.5">
          Create agent
          <span className="flex items-center gap-1.5">
            {Array.from({ length: STEP_COUNT }, (_, i) => (
              <span
                key={i}
                className={cn(
                  'h-2 w-2 rounded-full',
                  i + 1 <= step ? 'bg-accent' : 'bg-surface3',
                )}
              />
            ))}
          </span>
        </span>
      }
      footer={
        <>
          {canBack && (
            <Button variant="secondary" size="sm" onClick={() => setStep((s) => s - 1)}>
              Back
            </Button>
          )}
          <Button variant="primary" size="sm" disabled={!canAdvance || create.isPending} onClick={next}>
            {nextLabel}
          </Button>
        </>
      }
    >
      <div className="flex min-h-[220px] flex-col gap-3.5">
        {step === 1 && (
          <>
            <Field
              label="Agent name"
              hint="The name becomes the container identity and shows in the fleet grid and chat picker."
            >
              <Input
                autoFocus
                placeholder="e.g. churn-analyst"
                value={name}
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && step1Valid) setStep(2)
                }}
              />
            </Field>
          </>
        )}

        {step === 2 && (
          <>
            <p className="text-xs font-semibold text-text2">Data role (access floor)</p>
            <label
              className={cn(
                'flex cursor-pointer items-center gap-2.5 rounded-[9px] border px-3 py-2.5',
                roleMode === 'existing'
                  ? 'border-accent bg-accent-soft'
                  : 'border-border bg-surface',
              )}
            >
              <input
                type="radio"
                className="accent-accent"
                checked={roleMode === 'existing'}
                onChange={() => setRoleMode('existing')}
              />
              <span className="flex flex-1 flex-col">
                <span className="text-sm font-semibold">Use existing role</span>
                <span className="text-2xs text-text3">Pick from core.roles</span>
              </span>
              <Select
                className="w-auto"
                value={existingRole}
                onChange={(e) => {
                  setExistingRole(e.target.value)
                  setRoleMode('existing')
                }}
              >
                {EXISTING_ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </label>
            <label
              className={cn(
                'flex cursor-pointer items-center gap-2.5 rounded-[9px] border px-3 py-2.5',
                roleMode === 'new' ? 'border-accent bg-accent-soft' : 'border-border bg-surface',
              )}
            >
              <input
                type="radio"
                className="accent-accent"
                checked={roleMode === 'new'}
                onChange={() => setRoleMode('new')}
              />
              <span className="flex flex-col">
                <span className="text-sm font-semibold">Create floored role</span>
                <span className="text-2xs text-text3">
                  create_agent_role — deny-all floor, then allow per source
                </span>
              </span>
            </label>
          </>
        )}

        {step === 3 && (
          <>
            <p className="text-xs font-semibold text-text2">config_override (optional JSON)</p>
            <Textarea
              mono
              rows={6}
              spellCheck={false}
              className="bg-surface2 text-xs"
              value={override}
              onChange={(e) => setOverride(e.target.value)}
            />
            {overrideError && <Banner tone="error">Invalid JSON — {overrideError}</Banner>}
          </>
        )}

        {step === 4 && result && (
          <div className="flex flex-col items-center gap-2.5 py-1.5 text-center">
            <span className="flex h-9 w-9 items-center justify-center rounded-full bg-green-soft text-green">
              <Check className="h-5 w-5" />
            </span>
            <div className="text-sm font-bold">
              <span className="font-mono">{name.trim()}</span> provisioned
            </div>
            <p className="text-xs text-text2">
              One-time bootstrap secret — shown once, never retrievable again. The agent container
              uses it to register with the hub.
            </p>
            <Banner tone="reveal" className="flex w-full items-center gap-2">
              <span className="flex-1 break-all text-left font-mono text-xs">{result.secret}</span>
              <Button variant="secondary" size="sm" className="flex-none" onClick={copySecret}>
                {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </Banner>
            <p className="font-mono text-2xs text-text3">
              expires_at: {result.expires_at || '—'} · status: {result.status}
            </p>
          </div>
        )}
      </div>
    </Modal>
  )
}
