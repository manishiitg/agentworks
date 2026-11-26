import { useEffect, useState } from 'react'
import { SubagentChatPanel } from './SubagentChatPanel'
import { useChatDisplayStore } from '../../stores/useChatDisplayStore'

const instant = (value: unknown): number | undefined => {
  if (typeof value !== 'string' || value.startsWith('0001-')) return undefined
  const at = Date.parse(value)
  return Number.isFinite(at) ? at : undefined
}
const duration = (ms: number): string => {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m ${String(seconds % 60).padStart(2, '0')}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}
// What a background command is, in plain words (owner, 20 Nov: "make it simpler"). Claude's own task
// description ("Commit the changes") is used; anything that looks like a path or a raw command is not.
export function backgroundCommandName(record: Record<string, unknown>): string {
  const name = typeof record.name === 'string' ? record.name.trim() : ''
  if (!name || record.provider !== 'claude-code' || record.origin !== 'sdk_helper' || /[\\/]/.test(name) || name === record.command_label) return ''
  return name.length > 70 ? `${name.slice(0, 69)}…` : name
}

function commandLine(record: Record<string, unknown>): string {
  const name = backgroundCommandName(record)
  switch (record.state) {
    case 'started': return name ? `Running in the background: ${name}` : 'Running a command in the background'
    case 'finished': case 'completed': return name ? `Finished in the background: ${name}` : 'A background command finished'
    case 'stopped': return name ? `Stopped: ${name}` : 'A background command was stopped'
    case 'interrupted': return name ? `Interrupted: ${name}` : 'A background command was interrupted'
    case 'failed': return name ? `Didn't finish: ${name}` : "A background command didn't finish"
    default: return name ? `Ended: ${name}` : 'A background command ended'
  }
}

// The exact command is never on the line; it is the row's tooltip (owner, 20 Nov).
export function backgroundTaskLine(record: Record<string, unknown>, now: number): string {
  const helper = record.kind === 'helper'
  const helperType = typeof record.helper_type === 'string' ? record.helper_type.trim() : ''
  const kind = helper ? helperType || 'Background agent' : 'Background shell'
  const state = record.state === 'started' ? 'running…' : ['finished', 'completed'].includes(String(record.state)) ? 'finished' : record.state === 'stopped' ? 'stopped' : record.state === 'failed' ? 'failed' : record.state === 'interrupted' ? 'interrupted' : 'ended'
  const start = instant(record.started_at), end = instant(record.ended_at)
  const elapsed = start !== undefined && (record.state === 'started' || end !== undefined) ? (end ?? now) - start : typeof record.duration_ms === 'number' ? record.duration_ms : undefined
  // Only presentation is delayed: normal tool steps and native ownership stay intact.
  if (helper && record.helper_mode === 'foreground' && (elapsed === undefined || elapsed < 3000)) return ''
  const parts = [!helper ? commandLine(record) : helperType && record.state === 'started' ? kind : `${kind} ${state}`]
  if (helper) {
    const activity = record.state === 'started' && typeof record.current_step === 'string' && record.current_step.trim() ? record.current_step : record.helper_description
    if (typeof activity === 'string' && activity.trim()) parts.push(activity)
  }
  if (start !== undefined && (record.state === 'started' || end !== undefined)) parts.push(duration((end ?? now) - start))
  else if (typeof record.duration_ms === 'number' && record.duration_ms > 0) parts.push(duration(record.duration_ms))
  if (typeof record.tokens === 'number' && Number.isSafeInteger(record.tokens) && record.tokens >= 0) {
    const count = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(record.tokens).replace('K', 'k')
    parts.push(`${count} tokens`)
  }
  return parts.join(' · ')
}

// State, start/end and task-owned usage are server facts. This clock only
// formats elapsed time; it never ends a task or assigns a parent's tokens.
export function NativeBackgroundTaskLine({ record, sessionId }: { record: Record<string, unknown>; sessionId?: string }) {
  const showTools = useChatDisplayStore(state => state.showTools)
  const [open, setOpen] = useState(false)
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (record.state !== 'started') return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [record.work_id, record.state])
  const line = backgroundTaskLine(record, now)
  // A running background command is shown once, in the line above the message box with its Stop; the chat gets its
  // line when it ends (owner, 26 Nov: "one background task shows 3 times").
  if (!line || (record.kind === 'background_command' && record.state === 'started')) return null
  const dot = record.state === 'started' && <span className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-primary/70" aria-hidden="true" />
  const command = record.kind === 'background_command' && typeof record.command_label === 'string' ? record.command_label.trim() : ''
  const title = command && showTools ? command : (record.kind === 'helper' || showTools) && typeof record.name === 'string' ? record.name : undefined
  // A Claude subagent's row opens its own conversation beside the chat (#3132).
  const toolCallId = typeof record.tool_id === 'string' ? record.tool_id.trim() : ''
  if (sessionId && toolCallId && record.kind === 'helper' && record.provider === 'claude-code') return <>
    <button type="button" data-testid="native-background-task-line" data-background-agent-id={String(record.work_id)} onClick={() => setOpen(true)} title={title ? `${title} · open its conversation` : 'Open its conversation'}
      className="my-1 flex items-center gap-2 rounded text-left text-xs text-muted-foreground hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      {dot}<span>{line}</span>
    </button>
    {open && <SubagentChatPanel sessionId={sessionId} toolCallId={toolCallId} title={title || line} onClose={() => setOpen(false)} />}
  </>
  return <div data-testid="native-background-task-line" data-background-agent-id={String(record.work_id)} className="my-1 flex items-center gap-2 text-xs text-muted-foreground" title={title}>
    {dot}
    <span>{line}</span>
  </div>
}
