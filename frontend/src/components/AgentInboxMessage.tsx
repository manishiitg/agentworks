import React, { useState } from 'react'
import { Bot, ChevronDown, ChevronRight } from 'lucide-react'
import { ConversationMarkdownRenderer } from './ui/MarkdownRenderer'
import { PulseIcon } from './workflow/pulseIcon'
import { ShowFullContentButton, useChatArtifactText, type ChatArtifactRef } from './ChatArtifactExpander'

// Only server-stamped delivery metadata selects this presentation. Unmarked
// saved rows and typed envelope text remain ordinary messages.
export function agentInboxMessage(metadata?: Record<string, unknown>) {
  if (metadata?.source !== 'agent_message') return null
  const { sender_label: sender, agent_message_inbox: inboxID, message_id: messageID, agent_message_instructions: instructions, display_content: body } = metadata
  if ([sender, inboxID, messageID, instructions, body].some(value => typeof value !== 'string' || !value.trim())) return null
  return { sender: sender as string, kind: typeof metadata.sender_kind === 'string' ? metadata.sender_kind : '', inboxID: inboxID as string, messageID: messageID as string, instructions: instructions as string, body: body as string,
    bodyTruncated: typeof metadata.display_content_truncated === 'boolean' ? metadata.display_content_truncated : undefined }
}

type AgentInboxContent = NonNullable<ReturnType<typeof agentInboxMessage>>
const PREVIEW_LIMIT = 600
// Plain words for the sender kind the server stamps; an unknown or empty kind adds nothing.
const KIND_LABEL: Record<string, string> = { agent: 'Agent', crew: 'Crew', goal: 'Goal', flow: 'Flow', code: 'Crew', pulse: 'Pulse' }
const BODY_ARTIFACT_KEYS = ['metadata.display_content'] as const

export function AgentInboxMessage({ message, timestamp, artifact = null }: { message: AgentInboxContent; timestamp: string; artifact?: ChatArtifactRef | null }) {
  const [expanded, setExpanded] = useState(false)
  const full = useChatArtifactText(artifact, BODY_ARTIFACT_KEYS)
  const body = full.text || message.body
  const long = body.length > PREVIEW_LIMIT
  const Icon = message.kind === 'pulse' || /\bPulse(?: chat)?$/i.test(message.sender) ? PulseIcon : Bot
  const kind = KIND_LABEL[message.kind]
  const showKind = kind && !message.sender.toLowerCase().startsWith(kind.toLowerCase())
  return (
    <article data-testid="agent-inbox-message" className="my-3 w-[min(92%,52rem)] border-l-2 border-primary/50 pl-3 text-left">
      <header className="flex flex-wrap items-center gap-x-2 gap-y-0.5 pb-1 text-xs">
        <Icon className="h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 break-words font-medium text-foreground">{showKind ? `${kind} · ${message.sender}` : message.sender}</span>
        <span className="text-[11px] text-muted-foreground">Agent message</span>
        {timestamp && <span className="ml-auto shrink-0 tabular-nums text-muted-foreground">{timestamp}</span>}
      </header>
      <div className="min-w-0 pb-1">
        <div className="break-words text-[length:calc(14px*var(--chat-scale,1))] leading-[calc(22px*var(--chat-scale,1))]">
          {long && !expanded
            ? <p className="whitespace-pre-wrap">{body.slice(0, PREVIEW_LIMIT).trimEnd()}…</p>
            : <ConversationMarkdownRenderer content={body} framed={false} maxHeight="none" copyableCode />}
        </div>
        {long && <button type="button" onClick={() => setExpanded(value => !value)} aria-expanded={expanded}
          className="mt-2 rounded text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
          {expanded ? 'Show less' : 'Show full message'}
        </button>}
        {/* Legacy summaries lack the body marker: retain their existing access
            to full content rather than treating an unknown body as complete. */}
        {artifact && message.bodyTruncated !== false && !full.text && <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error}
          label="Load full message" onLoad={() => { void full.load().then(() => setExpanded(true)) }} />}
        <details className="group mt-2 text-xs text-muted-foreground">
          <summary className="flex w-fit cursor-pointer list-none items-center gap-1 rounded hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary [&::-webkit-details-marker]:hidden">
            <ChevronRight className="h-3 w-3 group-open:hidden" aria-hidden="true" />
            <ChevronDown className="hidden h-3 w-3 group-open:block" aria-hidden="true" />
            Details for developers
          </summary>
          <dl className="mt-2 grid gap-1">
            <dt className="font-medium">Reply address</dt><dd className="select-text break-all font-mono">{message.inboxID}</dd>
            <dt className="mt-1 font-medium">Message ID</dt><dd className="select-text break-all font-mono">{message.messageID}</dd>
          </dl>
          <p className="mt-2 whitespace-pre-wrap break-words leading-5">{message.instructions}</p>
        </details>
      </div>
    </article>
  )
}
