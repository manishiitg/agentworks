import { createContext, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { PollingEvent } from '../../services/api-types'
import { changedFileNames, fileChangesAfter, type RewindPreview } from '../../utils/editResend'

// Edit and resend (#3134, approved mockup "Edit and resend a message"): Edit
// on a message the person sent opens it in place; Resend takes the chat back
// to just before it (optionally reverting later file changes) and sends the
// new text. The chat that owns the transcript supplies these actions.
export type MessageEditActions = {
  events: PollingEvent[]
  /** Set while the chat is replying: Edit shows this instead of opening. */
  disabledReason?: string
  preview: (eventId: string) => Promise<RewindPreview>
  /** Rejects with { message, code } in plain words. */
  resend: (input: { eventId: string; sequence: number; text: string; revertFiles: boolean }) => Promise<void>
  putInComposer: (text: string) => void
}

export const MessageEditContext = createContext<MessageEditActions | null>(null)

const notInAgentCode = 'not_in_agent'

const appNames: Record<string, string> = { 'claude-code': 'Claude Code', 'codex-cli': 'Codex', 'cursor-cli': 'Cursor', 'agy-cli': 'Antigravity', 'pi-cli': 'Pi', 'muse-cli': 'Muse' }

function howItGoesBack(preview: RewindPreview | null): string {
  if (!preview) return ''
  const appName = appNames[preview.provider] || 'The agent'
  if (preview.native) return `${appName} goes back to its own session at that point.`
  return `${appName} can't go back on its own, so it starts fresh with the chat up to that point.`
}

export function EditMessageButton({ onEdit }: { onEdit: () => void }) {
  const actions = useContext(MessageEditContext)
  const [note, setNote] = useState(false)
  if (!actions) return null
  const blocked = Boolean(actions.disabledReason)
  return <>
    <button type="button" data-testid="edit-sent-message" onClick={() => blocked ? setNote(value => !value) : onEdit()} aria-disabled={blocked || undefined}
      className={`rounded px-1 font-medium transition-colors ${blocked ? 'cursor-not-allowed opacity-60' : 'hover:text-foreground'}`}>Edit</button>
    {blocked && note && <span role="status" className="basis-full text-right">{actions.disabledReason}</span>}
  </>
}

export function EditResendEditor({ eventId, sequence, text, onClose }: { eventId: string; sequence: number; text: string; onClose: () => void }) {
  const actions = useContext(MessageEditContext)
  const [draft, setDraft] = useState(text)
  const [revertFiles, setRevertFiles] = useState(false)
  const [preview, setPreview] = useState<RewindPreview | null>(null)
  const [refusal, setRefusal] = useState('')
  const [state, setState] = useState<'editing' | 'going' | 'failed'>('editing')
  const [error, setError] = useState('')
  const events = actions?.events
  const files = useMemo(() => events ? changedFileNames(events, sequence) : [], [events, sequence])
  const replies = useMemo(() => events ? fileChangesAfter(events, sequence).length : 0, [events, sequence])

  // The chat's actions change with every poll; check once per opened message.
  const previewRef = useRef(actions?.preview)
  previewRef.current = actions?.preview
  useEffect(() => {
    const preview = previewRef.current
    if (!preview) return
    let current = true
    preview(eventId)
      .then(result => { if (current) setPreview(result) })
      .catch((cause: { message?: string; code?: string }) => { if (current) setRefusal(cause?.message || 'This message cannot be edited right now.') })
    return () => { current = false }
  }, [eventId])

  if (!actions) return null
  const putInComposer = () => { actions.putInComposer(draft.trim() || text); onClose() }
  if (refusal) {
    return <div data-testid="edit-resend-refusal" className="mt-1 max-w-full text-right text-xs text-muted-foreground">
      <p>{refusal}</p>
      <div className="mt-1 flex justify-end gap-2">
        <button type="button" onClick={onClose} className="rounded-md border border-border px-2 py-1 hover:bg-muted">Close</button>
        <button type="button" onClick={putInComposer} className="rounded-md border border-border px-2 py-1 hover:bg-muted">Put the text in the message box</button>
      </div>
    </div>
  }
  const later = preview?.later_messages ?? 0
  const send = async () => {
    const next = draft.trim()
    if (!next) return
    setState('going')
    setError('')
    try {
      await actions.resend({ eventId, sequence, text: next, revertFiles })
      onClose()
    } catch (cause) {
      const failure = cause as { message?: string; code?: string }
      if (failure?.code === notInAgentCode) setRefusal(failure.message || '')
      setError(failure?.message || 'Couldn\'t go back. Nothing was changed.')
      setState('failed')
    }
  }
  return <div data-testid="edit-resend-editor" className="w-full min-w-[16rem] text-left">
    <textarea aria-label="Edit your message" value={draft} autoFocus disabled={state === 'going'}
      onChange={event => setDraft(event.target.value)}
      onKeyDown={event => { if (event.key === 'Escape') onClose(); if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) void send() }}
      className="min-h-[72px] w-full resize-y rounded-lg border border-border bg-background px-3 py-2 text-[length:calc(14px*var(--chat-scale,1))] text-foreground focus:outline-none focus:ring-1 focus:ring-ring" />
    {replies > 0 && <label className="mt-2 flex items-start gap-2 text-xs text-foreground">
      <input type="checkbox" checked={revertFiles} disabled={state === 'going'} onChange={event => setRevertFiles(event.target.checked)} className="mt-0.5" />
      <span>Also revert the file changes made after this message
        <span className="block text-muted-foreground">{files.slice(0, 6).join(', ')}{files.length > 6 ? ` and ${files.length - 6} more` : ''}{files.length ? ' · ' : ''}{replies === 1 ? '1 reply' : `${replies} replies`}</span>
      </span>
    </label>}
    <p className="mt-2 text-xs text-muted-foreground">
      {preview ? `${later === 0 ? 'Nothing after this message leaves the chat.' : later === 1 ? 'The message after this one will leave the chat.' : `The ${later} messages after this one will leave the chat.`} ${howItGoesBack(preview)}` : 'Checking…'}
    </p>
    {preview?.team_copy && <p data-testid="edit-resend-team-copy" className="mt-1 text-xs text-muted-foreground">The Team's backup copy of this chat is not changed: teammates who can read it still see the earlier messages.</p>}
    {state === 'going' && <p role="status" className="mt-1 text-xs text-muted-foreground">Going back to before this message{revertFiles ? ' and putting files back' : ''}…</p>}
    {state === 'failed' && <p role="alert" className="mt-1 text-xs text-destructive">{error} Your edited text is kept.</p>}
    <div className="mt-2 flex flex-wrap justify-end gap-2 text-xs">
      {state === 'failed' && <button type="button" onClick={putInComposer} className="rounded-md border border-border px-2 py-1 hover:bg-muted">Put the text in the message box</button>}
      <button type="button" onClick={onClose} disabled={state === 'going'} className="rounded-md border border-border px-2 py-1 hover:bg-muted">Cancel</button>
      <button type="button" data-testid="edit-resend-send" onClick={() => void send()} disabled={state === 'going' || !preview || !draft.trim()}
        className="rounded-md bg-foreground px-3 py-1 font-medium text-background disabled:opacity-50">{state === 'failed' ? 'Try again' : 'Resend'}</button>
    </div>
  </div>
}
