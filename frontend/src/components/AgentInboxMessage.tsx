import { useState, type ReactNode } from 'react'
import { Bot, ChevronDown } from 'lucide-react'
import { shouldCollapseTranscriptUserMessage, transcriptUserMessagePreview } from '../utils/terminalEventTranscript'
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

// Keep the server-attributed sender intact: display labels are not a protocol
// for guessing Crew, person, channel or agent identity. Details stay optional.
export function AgentInboxMessage({ message, timestamp, artifact = null, queueStatus }: { queueStatus?: ReactNode; message: AgentInboxContent; timestamp: string; artifact?: ChatArtifactRef | null }) {
  const full = useChatArtifactText(artifact, BODY_ARTIFACT_KEYS)
  const body = full.text || message.body
  const [expanded, setExpanded] = useState(false)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const hasDetails = !!(message.kind || message.inboxID || message.instructions)
  const collapsible = shouldCollapseTranscriptUserMessage(body)
  const shown = collapsible && !expanded ? transcriptUserMessagePreview(body) : body
  const needsLoad = artifact && message.bodyTruncated !== false && !full.text
  return (
    <article data-testid="agent-inbox-message" className="my-2 w-[min(100%,52rem)] overflow-hidden rounded-lg border border-border bg-muted/20 text-left">
      <header className="flex items-start gap-2 border-b border-border/60 px-3 py-1.5 text-xs text-muted-foreground">
        <Bot aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <span className="min-w-0 flex-1 break-words [overflow-wrap:anywhere] font-medium text-foreground">{message.sender}</span>
        {queueStatus}
        {timestamp && <time className="shrink-0 pt-0.5 text-[10px] tabular-nums">{timestamp}</time>}
        {hasDetails && <button type="button" aria-label="Message details" aria-expanded={detailsOpen}
          className="-m-0.5 shrink-0 rounded p-0.5 hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={() => setDetailsOpen(value => !value)}>
          <ChevronDown aria-hidden="true" className={`h-4 w-4 transition-transform ${detailsOpen ? 'rotate-180' : ''}`} />
        </button>}
      </header>
      {detailsOpen && <div className="space-y-1 border-b border-border/60 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
        {message.kind && <p>Source: {message.kind}</p>}
        {message.inboxID && <p className="break-all">Reply address: {message.inboxID}</p>}
        {message.instructions && <p className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{message.instructions}</p>}
      </div>}
      <div data-testid="agent-inbox-body" className="agent-inbox-prose min-w-0 max-w-full break-words [overflow-wrap:anywhere] px-3 py-2 text-left text-[length:calc(12.5px*var(--chat-scale,1))] leading-[calc(19px*var(--chat-scale,1))] text-foreground">
        <ConversationMarkdownRenderer content={shown} framed={false} maxHeight="none" copyableCode />
        {/* Legacy summaries lack the body marker: retain their existing access
            to full content rather than treating an unknown body as complete. */}
        {needsLoad && artifact ? <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error}
          label="Load full message" onLoad={() => { setExpanded(true); void full.load() }} /> : collapsible && <button type="button" aria-expanded={expanded}
          className="mt-1 text-[11px] font-medium text-muted-foreground hover:text-foreground" onClick={() => setExpanded(value => !value)}>
          {expanded ? 'Show less' : 'Show full message'}
        </button>}
      </div>
    </article>
  )
}
