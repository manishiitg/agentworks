import { History } from 'lucide-react'

const CONTINUITY_MARKER = '[AGENTWORKS CONVERSATION CONTINUITY]'

export function isConversationContinuityNotice(content: string): boolean {
  return content.trimStart().startsWith(CONTINUITY_MARKER)
}

export function ConversationContinuityNotice({ content, timestamp }: { content: string; timestamp?: string }) {
  return (
    <details className="my-1.5 w-full rounded-lg border border-sky-500/20 bg-sky-500/5 px-3 py-1 text-left" data-testid="conversation-continuity-notice">
      <summary className="flex cursor-pointer list-none items-center gap-2 text-xs text-muted-foreground [&::-webkit-details-marker]:hidden">
        <History className="h-3.5 w-3.5 shrink-0 text-sky-500" />
        <span className="font-medium text-foreground">Conversation restored</span>
        <span className="min-w-0 truncate">Previous conversation loaded</span>
        {timestamp && <span className="ml-auto shrink-0 tabular-nums text-[10px]">{timestamp}</span>}
      </summary>
      <div className="mt-2 border-t border-border/70 pt-2">
        <p className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Technical details</p>
        <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words font-mono text-[10px] leading-4 text-muted-foreground">{content}</pre>
      </div>
    </details>
  )
}
