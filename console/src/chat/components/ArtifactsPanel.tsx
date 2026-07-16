import { useRef } from 'react'
import type { Artifact } from '../frames'

export function ArtifactsPanel({
  artifacts,
  onUpload,
  onDownload,
}: {
  artifacts: Artifact[]
  onUpload: (file: File) => void
  onDownload: (a: Artifact) => void
}) {
  const fileRef = useRef<HTMLInputElement>(null)

  return (
    <div className="flex w-[264px] flex-none flex-col border-l border-border bg-surface animate-fadeUp">
      <div className="flex items-center border-b border-border px-3.5 py-2.5">
        <span className="flex-1 text-sm font-semibold">Artifacts</span>
        <button
          onClick={() => fileRef.current?.click()}
          className="rounded-md border border-border bg-surface px-2.5 py-0.5 text-xs font-medium text-text2 hover:bg-surface2"
        >
          Upload
        </button>
        <input
          ref={fileRef}
          type="file"
          className="hidden"
          onChange={(e) => {
            const f = e.target.files?.[0]
            if (f) onUpload(f)
            e.target.value = ''
          }}
        />
      </div>
      <div className="flex flex-1 flex-col gap-1.5 overflow-y-auto p-2.5">
        {artifacts.length === 0 ? (
          <div className="px-1 py-3 text-xs text-text3">No artifacts yet.</div>
        ) : (
          artifacts.map((a) => (
            <div key={a.id} className="flex flex-col gap-1 rounded-panel border border-border px-2.5 py-2">
              <div className="break-all font-mono text-xs font-semibold">{a.name}</div>
              <div className="flex items-center gap-1.5 text-2xs text-text3">
                {a.size && <span>{a.size}</span>}
                {a.by && (
                  <>
                    <span>·</span>
                    <span>by {a.by}</span>
                  </>
                )}
                {a.time && (
                  <>
                    <span>·</span>
                    <span>{a.time}</span>
                  </>
                )}
                <span className="flex-1" />
                <button
                  onClick={() => onDownload(a)}
                  className="text-xs font-semibold text-accent"
                >
                  ↓ Get
                </button>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
