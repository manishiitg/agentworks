import { useState } from 'react'
import { Bot } from 'lucide-react'
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

// A server-attributed incoming message. Standard and native chat share this
// body-first presentation; transport instructions never become the preview.
export function AgentInboxMessage({ message, timestamp, artifact = null }: { message: AgentInboxContent; timestamp: string; artifact?: ChatArtifactRef | null }) {
  const full = useChatArtifactText(artifact, BODY_ARTIFACT_KEYS)
  const body = full.text || message.body
  const [expanded, setExpanded] = useState(false)
  const collapsible = shouldCollapseTranscriptUserMessage(body)
  const shown = collapsible && !expanded ? transcriptUserMessagePreview(body) : body
  const needsLoad = artifact && message.bodyTruncated !== false && !full.text
  return (
    <article data-testid="agent-inbox-message" className="mb-3 mt-3 w-[min(100%,52rem)] overflow-hidden rounded-xl border border-border bg-muted/20 text-left">
      <header className="flex flex-wrap items-center gap-2 border-b border-border/60 px-4 py-2 text-xs text-muted-foreground">
        <Bot aria-hidden="true" className="h-4 w-4 shrink-0" />
        <span className="min-w-0 break-words font-medium text-foreground">{message.sender}</span>
        {message.kind && <span>{message.kind}</span>}
        {timestamp && <time className="ml-auto tabular-nums">{timestamp}</time>}
      </header>
      <div data-testid="agent-inbox-body" className="min-w-0 max-w-full break-words [overflow-wrap:anywhere] px-4 py-3 text-left text-[length:calc(14px*var(--chat-scale,1))] leading-[calc(20px*var(--chat-scale,1))] text-foreground">
        <ConversationMarkdownRenderer content={shown} framed={false} maxHeight="none" copyableCode />
        {/* Legacy summaries lack the body marker: retain their existing access
            to full content rather than treating an unknown body as complete. */}
        {needsLoad && artifact ? <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error}
          label="Load full message" onLoad={() => { setExpanded(true); void full.load() }} /> : collapsible && <button type="button" aria-expanded={expanded}
          className="mt-1 text-[11px] font-medium text-muted-foreground hover:text-foreground" onClick={() => setExpanded(value => !value)}>
          {expanded ? 'Show less' : 'Show full message'}
        </button>}
      </div>
      {(message.inboxID || message.instructions) && <details className="border-t border-border/60 px-4 py-2 text-xs text-muted-foreground">
        <summary className="cursor-pointer">Message details</summary>
        {message.inboxID && <p className="mt-2 break-all">Reply address: {message.inboxID}</p>}
        {message.instructions && <p className="mt-2 whitespace-pre-wrap break-words">{message.instructions}</p>}
      </details>}
    </article>
  )
}
