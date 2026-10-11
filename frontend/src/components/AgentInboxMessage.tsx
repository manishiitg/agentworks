import React, { useState } from 'react'
import { Bot, ChevronDown, ChevronRight } from 'lucide-react'
import { ConversationMarkdownRenderer } from './ui/MarkdownRenderer'
import { PulseIcon } from './workflow/pulseIcon'

// Only server-stamped delivery metadata selects this presentation. Unmarked
// saved rows and typed envelope text remain ordinary messages.
export function agentInboxMessage(metadata?: Record<string, unknown>) {
  if (metadata?.source !== 'agent_message') return null
  const { sender_label: sender, agent_message_inbox: inboxID, message_id: messageID, agent_message_instructions: instructions, display_content: body } = metadata
  if ([sender, inboxID, messageID, instructions, body].some(value => typeof value !== 'string' || !value.trim())) return null
  return { sender: sender as string, inboxID: inboxID as string, messageID: messageID as string, instructions: instructions as string, body: body as string }
}

type AgentInboxContent = NonNullable<ReturnType<typeof agentInboxMessage>>
const PREVIEW_LIMIT = 600

export function AgentInboxMessage({ message, timestamp }: { message: AgentInboxContent; timestamp: string }) {
  const [expanded, setExpanded] = useState(false)
  const long = message.body.length > PREVIEW_LIMIT
  const Icon = /\bPulse(?: chat)?$/i.test(message.sender) ? PulseIcon : Bot
  return (
    <article data-testid="agent-inbox-message" className="my-3 w-[min(92%,52rem)] overflow-hidden rounded-lg border border-primary/20 bg-primary/[0.03] text-left">
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-primary/10 px-4 py-2 text-xs">
        <Icon className="h-4 w-4 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 break-words font-medium text-foreground">{message.sender}</span>
        <span className="text-[11px] text-muted-foreground">Agent message</span>
        {timestamp && <span className="ml-auto shrink-0 tabular-nums text-muted-foreground">{timestamp}</span>}
      </header>
      <div className="min-w-0 px-4 py-3">
        <div className="break-words text-[length:calc(14px*var(--chat-scale,1))] leading-[calc(22px*var(--chat-scale,1))]">
          {long && !expanded
            ? <p className="whitespace-pre-wrap">{message.body.slice(0, PREVIEW_LIMIT).trimEnd()}…</p>
            : <ConversationMarkdownRenderer content={message.body} framed={false} maxHeight="none" copyableCode />}
        </div>
        {long && <button type="button" onClick={() => setExpanded(value => !value)} aria-expanded={expanded}
          className="mt-2 rounded text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
          {expanded ? 'Show less' : 'Show full message'}
        </button>}
        <details className="group mt-3 border-t border-border/60 pt-2 text-xs text-muted-foreground">
          <summary className="flex w-fit cursor-pointer list-none items-center gap-1 rounded hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary [&::-webkit-details-marker]:hidden">
            <ChevronRight className="h-3 w-3 group-open:hidden" aria-hidden="true" />
            <ChevronDown className="hidden h-3 w-3 group-open:block" aria-hidden="true" />
            Message details
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
