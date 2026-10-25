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
// Plain words for the sender kind the server stamps; an unknown or empty kind adds nothing.
const KIND_LABEL: Record<string, string> = { agent: 'Agent', crew: 'Crew', goal: 'Goal', flow: 'Flow', code: 'Crew', pulse: 'Autopilot' }
const BODY_ARTIFACT_KEYS = ['metadata.display_content'] as const

// A message another agent sent into this chat: one quiet row, like "Message to X sent", opened on request.
export function AgentInboxMessage({ message, timestamp, artifact = null }: { message: AgentInboxContent; timestamp: string; artifact?: ChatArtifactRef | null }) {
  const [open, setOpen] = useState(false)
  const full = useChatArtifactText(artifact, BODY_ARTIFACT_KEYS)
  const body = full.text || message.body
  const Icon = message.kind === 'pulse' || /\b(?:Pulse|Autopilot)(?: chat)?$/i.test(message.sender) ? PulseIcon : Bot
  const kind = KIND_LABEL[message.kind]
  const showKind = kind && !message.sender.toLowerCase().startsWith(kind.toLowerCase())
  return (
    <article data-testid="agent-inbox-message" className="my-3 border-y border-border/60 py-2 text-left">
      <button type="button" onClick={() => setOpen(value => !value)} aria-expanded={open}
        className="flex w-full items-center gap-2 text-left text-[11px] text-muted-foreground transition-colors hover:text-foreground">
        <Icon className="h-3.5 w-3.5 shrink-0 text-primary/70" aria-hidden="true" />
        <span className="truncate">Message from <span className="font-medium text-foreground/85">{showKind ? `${kind} · ${message.sender}` : message.sender}</span></span>
        {timestamp && <span className="ml-auto shrink-0 tabular-nums">{timestamp}</span>}
        {open ? <ChevronDown className={`${timestamp ? '' : 'ml-auto '}h-3.5 w-3.5 shrink-0`} /> : <ChevronRight className={`${timestamp ? '' : 'ml-auto '}h-3.5 w-3.5 shrink-0`} />}
      </button>
      {open && <div className="mt-2 max-h-56 overflow-auto rounded-lg bg-muted/60 p-3 [&_li]:!text-[12px] [&_p]:!text-[12px]">
        <div data-testid="agent-inbox-body" className="min-w-0 break-words">
          <ConversationMarkdownRenderer content={body} framed={false} maxHeight="none" copyableCode />
        </div>
        {/* Legacy summaries lack the body marker: retain their existing access
            to full content rather than treating an unknown body as complete. */}
        {artifact && message.bodyTruncated !== false && !full.text && <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error}
          label="Load full message" onLoad={() => { void full.load() }} />}
      </div>}
    </article>
  )
}
