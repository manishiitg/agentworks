import { useState, type ReactNode } from 'react'
import { Bot, ChevronDown } from 'lucide-react'
import { ConversationMarkdownRenderer } from './ui/MarkdownRenderer'
import { ShowFullContentButton, useChatArtifactText, type ChatArtifactRef } from './ChatArtifactExpander'

// Only server-stamped delivery metadata selects this presentation. Unmarked
// saved rows and typed envelope text remain ordinary messages.
export function agentInboxMessage(metadata?: Record<string, unknown>) {
  if (metadata?.source !== 'agent_message') return null
  const { sender_label: sender, agent_message_inbox: inboxID, message_id: messageID, agent_message_instructions: instructions, display_content: body } = metadata
  if ([sender, body].some(value => typeof value !== 'string' || !value.trim())) return null
  return { sender: sender as string, kind: typeof metadata.sender_kind === 'string' ? metadata.sender_kind : typeof metadata.sender_channel === 'string' ? metadata.sender_channel : '', inboxID: typeof inboxID === 'string' ? inboxID : '', messageID: typeof messageID === 'string' ? messageID : '', instructions: typeof instructions === 'string' ? instructions : '', body: body as string,
    bodyTruncated: typeof metadata.display_content_truncated === 'boolean' ? metadata.display_content_truncated : undefined }
}

type AgentInboxContent = NonNullable<ReturnType<typeof agentInboxMessage>>
const BODY_ARTIFACT_KEYS = ['metadata.display_content'] as const

// One quiet line per message: agent-to-agent updates arrive often and most need nothing from the person, so the
// body stays closed until clicked (PLAT-746). The server-attributed sender is shown as is; display labels are not a
// protocol for guessing Crew, person, channel or agent identity. Details stay optional.
export function agentInboxPreview(body: string) {
  const line = body.split('\n').map(part => part.replace(/[#>*_`]/g, '').trim()).find(Boolean) || ''
  return line.length > 160 ? `${line.slice(0, 160)}…` : line
}

export function AgentInboxMessage({ message, timestamp, artifact = null, queueStatus }: { queueStatus?: ReactNode; message: AgentInboxContent; timestamp: string; artifact?: ChatArtifactRef | null }) {
  const full = useChatArtifactText(artifact, BODY_ARTIFACT_KEYS)
  const body = full.text || message.body
  const [open, setOpen] = useState(false)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const hasDetails = !!(message.kind || message.inboxID || message.instructions)
  const needsLoad = artifact && message.bodyTruncated !== false && !full.text
  return (
    <article data-testid="agent-inbox-message" className="my-1 w-[min(100%,52rem)] overflow-hidden rounded-lg border border-border/60 text-left">
      <header className="flex items-center gap-2 px-3 py-2 text-[11px] text-muted-foreground">
        <button type="button" aria-expanded={open} aria-label={open ? 'Hide message' : 'Show message'}
          className="flex min-w-0 flex-1 items-center gap-2 text-left hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={() => setOpen(value => !value)}>
          <Bot aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
          <span className="shrink-0 font-medium text-foreground/85">{message.sender}</span>
          {!open && <span className="min-w-0 flex-1 truncate">{agentInboxPreview(body)}</span>}
        </button>
        {queueStatus}
        {timestamp && <time className="shrink-0 text-[10px] tabular-nums">{timestamp}</time>}
        <ChevronDown aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </header>
      {open && <>
        {hasDetails && <div className="border-t border-border/60 px-3 py-1 text-[11px] text-muted-foreground">
          <button type="button" aria-expanded={detailsOpen} className="hover:text-foreground" onClick={() => setDetailsOpen(value => !value)}>
            {detailsOpen ? 'Hide details' : 'Details'}
          </button>
          {detailsOpen && <div className="mt-1 space-y-1">
            {message.kind && <p>Source: {message.kind}</p>}
            {message.inboxID && <p className="break-all">Reply address: {message.inboxID}</p>}
            {message.instructions && <p className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{message.instructions}</p>}
          </div>}
        </div>}
        <div data-testid="agent-inbox-body" className="agent-inbox-prose min-w-0 max-w-full break-words [overflow-wrap:anywhere] border-t border-border/60 px-3 py-2 text-left text-[length:calc(12.5px*var(--chat-scale,1))] leading-[calc(19px*var(--chat-scale,1))] text-foreground">
          <ConversationMarkdownRenderer content={body} framed={false} maxHeight="none" copyableCode />
          {/* Legacy summaries lack the body marker: retain their existing access
              to full content rather than treating an unknown body as complete. */}
          {needsLoad && artifact && <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error}
            label="Load full message" onLoad={() => { void full.load() }} />}
        </div>
      </>}
    </article>
  )
}
