import { useMemo } from 'react'
import { Input, Select, Field, Banner, JsonEditor } from '@/components/ui'

/**
 * Agent-type config editor: a CodeMirror JSON editor over the WHOLE config blob
 * (robust to the full, lenient hub+hugen schema) plus quick fields for the two
 * keys that are load-bearing — `orchestration.image` (spawn) and `models.model`
 * (hugen boot) — and the container resource caps. The JSON is the source of
 * truth; quick fields read/patch it by path and are disabled while it's invalid.
 */

type Obj = Record<string, unknown>

function getStr(o: Obj | null, path: string[]): string {
  let cur: unknown = o
  for (const k of path) {
    if (cur == null || typeof cur !== 'object') return ''
    cur = (cur as Obj)[k]
  }
  return cur == null ? '' : String(cur)
}

// Immutably set (or delete, when val is '') a nested path, cloning along the way.
function setPath(root: Obj, path: string[], val: unknown): Obj {
  const out: Obj = { ...root }
  let cur: Obj = out
  for (let i = 0; i < path.length - 1; i++) {
    const k = path[i]
    const child = cur[k]
    cur[k] = child && typeof child === 'object' ? { ...(child as Obj) } : {}
    cur = cur[k] as Obj
  }
  const last = path[path.length - 1]
  if (val === '' || val == null) delete cur[last]
  else cur[last] = val
  return out
}

export interface AgentConfigEditorProps {
  value: string
  onChange: (value: string) => void
  height?: number
}

export function AgentConfigEditor({ value, onChange, height = 320 }: AgentConfigEditorProps) {
  const parsed = useMemo<Obj | null>(() => {
    try {
      const o = value.trim() ? JSON.parse(value) : {}
      return o && typeof o === 'object' && !Array.isArray(o) ? (o as Obj) : null
    } catch {
      return null
    }
  }, [value])

  const editable = parsed !== null

  const patch = (path: string[], raw: string, numeric = false) => {
    if (!parsed) return
    let val: unknown = raw
    if (numeric) {
      if (raw.trim() === '') val = ''
      else {
        const n = parseInt(raw, 10)
        if (Number.isNaN(n)) return
        val = n
      }
    }
    onChange(JSON.stringify(setPath(parsed, path, val), null, 2))
  }

  const image = getStr(parsed, ['orchestration', 'image'])
  const model = getStr(parsed, ['models', 'model'])
  const mode = getStr(parsed, ['models', 'mode']) || 'remote'
  const logLevel = getStr(parsed, ['orchestration', 'env', 'HUGEN_LOG_LEVEL'])

  return (
    <div className="flex flex-col gap-3">
      {!editable && (
        <Banner tone="error">Config is not valid JSON — fix it below to use the quick fields.</Banner>
      )}

      <div className="grid grid-cols-2 gap-3">
        <Field label="Container image" hint="config.orchestration.image — required to spawn">
          <Input
            value={image}
            disabled={!editable}
            placeholder="hugen:latest"
            onChange={(e) => patch(['orchestration', 'image'], e.target.value)}
          />
        </Field>
        <Field label="Default model" hint="config.models.model — required for boot">
          <Input
            value={model}
            disabled={!editable}
            placeholder="gemma4-26b"
            onChange={(e) => patch(['models', 'model'], e.target.value)}
          />
        </Field>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <Field label="Mode">
          <Select value={mode} disabled={!editable} onChange={(e) => patch(['models', 'mode'], e.target.value)}>
            <option value="remote">remote</option>
            <option value="local">local</option>
          </Select>
        </Field>
        <Field label="Log level" hint="orchestration.env.HUGEN_LOG_LEVEL — applied on next start">
          <Select
            value={logLevel}
            disabled={!editable}
            onChange={(e) => patch(['orchestration', 'env', 'HUGEN_LOG_LEVEL'], e.target.value)}
          >
            <option value="">default</option>
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </Select>
        </Field>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Field label="Memory (bytes)" hint="0 = default">
          <Input
            value={getStr(parsed, ['orchestration', 'memory_bytes'])}
            disabled={!editable}
            placeholder="0"
            onChange={(e) => patch(['orchestration', 'memory_bytes'], e.target.value, true)}
          />
        </Field>
        <Field label="CPU (nanoCPUs)" hint="0 = default">
          <Input
            value={getStr(parsed, ['orchestration', 'nano_cpus'])}
            disabled={!editable}
            placeholder="0"
            onChange={(e) => patch(['orchestration', 'nano_cpus'], e.target.value, true)}
          />
        </Field>
        <Field label="PIDs limit" hint="0 = default">
          <Input
            value={getStr(parsed, ['orchestration', 'pids_limit'])}
            disabled={!editable}
            placeholder="0"
            onChange={(e) => patch(['orchestration', 'pids_limit'], e.target.value, true)}
          />
        </Field>
      </div>

      {editable && !image && (
        <Banner tone="error">
          Container image is empty — the agent cannot spawn (config.orchestration.image).
        </Banner>
      )}
      {editable && !model && (
        <Banner tone="error">
          Default model is empty — hugen will fail to boot (config.models.model).
        </Banner>
      )}

      <div>
        <p className="mb-1 text-2xs text-text3">
          Full config — models · skills · tool_providers · subagents · hitl · compactor · recap ·
          orchestration
        </p>
        <JsonEditor value={value} onChange={onChange} height={height} />
      </div>
    </div>
  )
}
