import { useState } from 'react'
import { shouldCollapseTranscriptUserMessage, transcriptUserMessagePreview } from '../utils/terminalEventTranscript'
import { ConversationMarkdownRenderer } from './ui/MarkdownRenderer'
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
const BODY_ARTIFACT_KEYS = ['metadata.display_content'] as const

// A message another agent sent into this chat (owner, 5 Nov): shown like a typed message, a bubble on the right, with
// who sent it and the time in small text under it.
export function AgentInboxMessage({ message, timestamp, artifact = null }: { message: AgentInboxContent; timestamp: string; artifact?: ChatArtifactRef | null }) {
  const full = useChatArtifactText(artifact, BODY_ARTIFACT_KEYS)
  const body = full.text || message.body
  const [expanded, setExpanded] = useState(false)
  const collapsible = shouldCollapseTranscriptUserMessage(body)
  const shown = collapsible && !expanded ? transcriptUserMessagePreview(body) : body
  const needsLoad = artifact && message.bodyTruncated !== false && !full.text
  return (
    <article data-testid="agent-inbox-message" className="mb-2 ml-auto mt-2 flex max-w-[90%] flex-col items-end text-right md:max-w-[75%]">
      <div data-testid="agent-inbox-body" className="max-w-full break-words rounded-2xl bg-muted px-4 py-3 text-left text-[length:calc(14px*var(--chat-scale,1))] leading-[calc(20px*var(--chat-scale,1))] text-foreground">
        <ConversationMarkdownRenderer content={shown} framed={false} maxHeight="none" copyableCode />
        {/* Legacy summaries lack the body marker: retain their existing access
            to full content rather than treating an unknown body as complete. */}
        {needsLoad && artifact ? <ShowFullContentButton ref_={artifact} loading={full.loading} error={full.error}
          label="Load full message" onLoad={() => { setExpanded(true); void full.load() }} /> : collapsible && <button type="button" aria-expanded={expanded}
          className="mt-1 text-[11px] font-medium text-muted-foreground hover:text-foreground" onClick={() => setExpanded(value => !value)}>
          {expanded ? 'Show less' : 'Show full message'}
        </button>}
      </div>
      <div className="mt-0.5 flex flex-wrap items-center justify-end gap-x-2 text-[10px] leading-4 text-muted-foreground">
        <span>From {message.sender}</span>
        {timestamp && <span className="tabular-nums">{timestamp}</span>}
      </div>
    </article>
  )
}
