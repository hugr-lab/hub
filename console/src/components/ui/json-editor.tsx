import { useMemo } from 'react'
import CodeMirror, { EditorView } from '@uiw/react-codemirror'
import { json, jsonParseLinter } from '@codemirror/lang-json'
import { linter, lintGutter } from '@codemirror/lint'
import { oneDark } from '@codemirror/theme-one-dark'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/cn'

/**
 * CodeMirror-backed JSON editor: syntax highlighting, bracket matching, code
 * folding, and live parse-error linting (gutter marker + squiggle). The console
 * uses it for agent-type / agent config. Self-contained (CM6, no CDN/eval).
 */
export interface JsonEditorProps {
  value: string
  onChange: (value: string) => void
  height?: number
  readOnly?: boolean
  className?: string
}

// Transparent chrome so the editor inherits the console surface + font size.
const chrome = EditorView.theme({
  '&': { fontSize: '12px', backgroundColor: 'transparent' },
  '.cm-scroller': { fontFamily: 'var(--font-mono, ui-monospace, monospace)' },
  '.cm-gutters': { backgroundColor: 'transparent', borderRight: '1px solid var(--border)' },
  '&.cm-focused': { outline: 'none' },
})

export function JsonEditor({ value, onChange, height = 340, readOnly, className }: JsonEditorProps) {
  const { theme } = useTheme()
  const extensions = useMemo(() => [json(), linter(jsonParseLinter()), lintGutter(), chrome], [])
  return (
    <div className={cn('overflow-hidden rounded-btn border border-border bg-surface2', className)}>
      <CodeMirror
        value={value}
        onChange={onChange}
        height={`${height}px`}
        theme={theme === 'dark' ? oneDark : 'light'}
        readOnly={readOnly}
        extensions={extensions}
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: false,
          highlightActiveLineGutter: false,
        }}
      />
    </div>
  )
}

/** Parse-error message for `text`, or null when it is valid JSON (empty = ok). */
export function jsonParseError(text: string): string | null {
  if (!text.trim()) return null
  try {
    JSON.parse(text)
    return null
  } catch (e) {
    return e instanceof Error ? e.message : 'Invalid JSON'
  }
}
