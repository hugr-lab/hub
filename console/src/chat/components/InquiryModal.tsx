import { useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Input } from '@/components/ui'
import type { InquiryAnswer } from '../client'
import type { Inquiry } from '../frames'

/**
 * HITL inquiry modal. Approval → reject / approve+auto / approve. Clarification
 * (and research_batch) → a question/answers form. Blocking: no backdrop close.
 */
export function InquiryModal({
  inquiry,
  onAnswer,
}: {
  inquiry: Inquiry
  onAnswer: (a: InquiryAnswer) => void
}) {
  const isApproval = inquiry.type === 'approval'
  return (
    <div className="fixed inset-0 z-[150] flex items-center justify-center bg-[rgba(10,16,15,0.45)] backdrop-blur-[2px]">
      <div
        role="dialog"
        aria-modal="true"
        className="flex w-[440px] max-w-[92vw] flex-col gap-3.5 rounded-modal border border-border bg-surface p-[22px] text-text shadow-lg animate-fadeUp"
      >
        <div className="flex items-center gap-2.5">
          <span className="flex h-8 w-8 flex-none items-center justify-center rounded-[9px] bg-amber-soft text-amber">
            <AlertTriangle className="h-4 w-4" />
          </span>
          <div className="flex flex-col">
            <span className="text-sm font-bold">
              {isApproval ? 'Approval required' : 'The agent needs your input'}
            </span>
            <span className="text-xs text-text3">The agent paused and needs your decision</span>
          </div>
        </div>

        {isApproval ? (
          <ApprovalBody inquiry={inquiry} onAnswer={onAnswer} />
        ) : (
          <ClarificationBody inquiry={inquiry} onAnswer={onAnswer} />
        )}
      </div>
    </div>
  )
}

function ApprovalBody({ inquiry, onAnswer }: { inquiry: Inquiry; onAnswer: (a: InquiryAnswer) => void }) {
  const [reason, setReason] = useState('')
  return (
    <>
      {inquiry.question && <div className="text-[13px] font-medium">{inquiry.question}</div>}
      {inquiry.context && (
        <div className="rounded-[9px] border border-border bg-surface2 px-3 py-2 text-xs leading-relaxed text-text2">
          {inquiry.context}
        </div>
      )}
      <Input
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="Optional reason (sent on reject)"
      />
      <div className="flex gap-2">
        <button
          onClick={() => onAnswer({ request_id: inquiry.request_id, approved: false, reason: reason || undefined })}
          className="flex-1 rounded-btn border border-border2 bg-surface py-2 text-xs font-semibold text-red hover:bg-red-soft"
        >
          Reject
        </button>
        <button
          onClick={() => onAnswer({ request_id: inquiry.request_id, approved: true, auto_approve_tools: true })}
          className="flex-[1.4] rounded-btn border border-border2 bg-surface py-2 text-xs font-semibold text-text hover:bg-surface2"
        >
          Approve + auto-approve tool
        </button>
        <button
          onClick={() => onAnswer({ request_id: inquiry.request_id, approved: true })}
          className="flex-1 rounded-btn bg-accent py-2 text-xs font-semibold text-accent-text hover:bg-accent-hi"
        >
          Approve
        </button>
      </div>
    </>
  )
}

function ClarificationBody({ inquiry, onAnswer }: { inquiry: Inquiry; onAnswer: (a: InquiryAnswer) => void }) {
  const clarifications = inquiry.clarifications ?? []
  const [text, setText] = useState('')
  const [answers, setAnswers] = useState<Record<string, string>>({})

  const submit = () => {
    if (clarifications.length > 0) {
      onAnswer({
        request_id: inquiry.request_id,
        answers: Object.fromEntries(Object.entries(answers).map(([k, v]) => [k, { value: v }])),
      })
    } else {
      onAnswer({ request_id: inquiry.request_id, response: text })
    }
  }

  return (
    <>
      {inquiry.question && <div className="text-[13px] font-medium">{inquiry.question}</div>}

      {clarifications.length > 0 ? (
        <div className="flex flex-col gap-3">
          {clarifications.map((c) => (
            <div key={c.id} className="flex flex-col gap-1">
              <div className="text-xs font-medium text-text2">
                {c.question}
                {c.kind === 'optional' && <span className="ml-1 text-text3">(optional)</span>}
              </div>
              {c.options && c.options.length > 0 ? (
                <div className="flex flex-wrap gap-1.5">
                  {c.options.map((opt) => (
                    <button
                      key={opt}
                      onClick={() => setAnswers((a) => ({ ...a, [c.id]: opt }))}
                      className={
                        'rounded-chip border px-2 py-1 text-xs ' +
                        (answers[c.id] === opt
                          ? 'border-accent bg-accent-soft text-accent'
                          : 'border-border bg-surface text-text2 hover:bg-surface2')
                      }
                    >
                      {opt}
                    </button>
                  ))}
                </div>
              ) : (
                <Input
                  value={answers[c.id] ?? ''}
                  onChange={(e) => setAnswers((a) => ({ ...a, [c.id]: e.target.value }))}
                  placeholder="Your answer"
                />
              )}
            </div>
          ))}
        </div>
      ) : inquiry.options && inquiry.options.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {inquiry.options.map((opt) => (
            <button
              key={opt}
              onClick={() => onAnswer({ request_id: inquiry.request_id, response: opt })}
              className="rounded-chip border border-border bg-surface px-2.5 py-1 text-xs text-text2 hover:bg-surface2"
            >
              {opt}
            </button>
          ))}
        </div>
      ) : (
        <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="Your answer" />
      )}

      {(clarifications.length > 0 || !inquiry.options?.length) && (
        <button
          onClick={submit}
          className="self-end rounded-btn bg-accent px-4 py-2 text-xs font-semibold text-accent-text hover:bg-accent-hi"
        >
          Send
        </button>
      )}
    </>
  )
}
