import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import {
  Modal,
  Button,
  Input,
  Select,
  Field,
  Banner,
  JsonEditor,
  useToast,
} from '@/components/ui'
import { cn } from '@/lib/cn'
import { useSession } from '@/lib/session'
import { createAgent, type CreateAgentResult } from '@/api/agents'
import { listAgentTypes } from '@/api/agent-types'
import { listRoles } from '@/api/platform-roles'

type RoleMode = 'existing' | 'new'

const STEP_COUNT = 4

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export function CreateAgentWizard({
  open,
  onOpenChange,
  preset,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  /** Seed values for "copy agent" — the wizard should be remounted (keyed) per source. */
  preset?: { name?: string; agentTypeId?: string; configOverride?: string }
}) {
  const qc = useQueryClient()
  const { success, error } = useToast()
  const { identity } = useSession()

  const agentTypes = useQuery({ queryKey: ['agentTypes'], queryFn: listAgentTypes })
  const roles = useQuery({ queryKey: ['roles'], queryFn: listRoles })

  const [step, setStep] = useState(1)
  const [name, setName] = useState(preset?.name ?? '')
  const [agentTypeId, setAgentTypeId] = useState(preset?.agentTypeId ?? '')
  const [roleMode, setRoleMode] = useState<RoleMode>('existing')
  const [existingRole, setExistingRole] = useState('')
  const [override, setOverride] = useState(preset?.configOverride ?? '{}')
  const [result, setResult] = useState<CreateAgentResult | null>(null)
  const [copied, setCopied] = useState(false)

  // Default the selectors to the first real option once the data arrives.
  useEffect(() => {
    if (!agentTypeId && agentTypes.data?.length) setAgentTypeId(agentTypes.data[0].id)
  }, [agentTypes.data, agentTypeId])
  useEffect(() => {
    if (!existingRole && roles.data?.length) setExistingRole(roles.data[0].name)
  }, [roles.data, existingRole])

  const reset = () => {
    setStep(1)
    setName(preset?.name ?? '')
    setAgentTypeId(preset?.agentTypeId ?? agentTypes.data?.[0]?.id ?? '')
    setRoleMode('existing')
    setExistingRole(roles.data?.[0]?.name ?? '')
    setOverride(preset?.configOverride ?? '{}')
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
        agent_type_id: agentTypeId,
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

  const step1Valid = name.trim().length > 0 && !!agentTypeId
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
            <Field
              label="Agent type"
              hint="The template (model, skills, container image) the agent is provisioned from — hub.agent.db.agent_types."
            >
              {agentTypes.isLoading ? (
                <div className="text-2xs text-text3">Loading agent types…</div>
              ) : agentTypes.data && agentTypes.data.length > 0 ? (
                <Select value={agentTypeId} onChange={(e) => setAgentTypeId(e.target.value)}>
                  {agentTypes.data.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} ({t.id})
                    </option>
                  ))}
                </Select>
              ) : (
                <Banner tone="error">
                  No agent types defined. Create one in Agents → Types before provisioning an agent.
                </Banner>
              )}
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
                {(roles.data ?? []).map((r) => (
                  <option key={r.name} value={r.name}>
                    {r.name}
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
            <p className="text-xs font-semibold text-text2">
              config_override (optional JSON) — merged over the agent type's config
            </p>
            <JsonEditor value={override} onChange={setOverride} height={220} />
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
