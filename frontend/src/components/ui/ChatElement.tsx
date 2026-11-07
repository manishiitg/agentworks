import { createContext, lazy, Suspense, type ReactNode } from 'react'

// Keep the transcript's reading choice through live-to-saved message remounts.
export const ChatElementDisclosureContext = createContext<{ values: Map<string, boolean>; scope: string } | null>(null)
export type ChatElementStream = 'progressive' | 'pending'
export type ChatVisualProps = { source: string; stream?: ChatElementStream; renderMarkdown: (body: string) => ReactNode; disclosureKey?: number }

const ChatVisualRenderer = lazy(() => import('./ChatVisualRenderer'))

export function ChatVisualPending() {
  return <div className="not-prose my-4 min-w-0 max-w-xl rounded-xl border border-border bg-muted/20 px-4 py-3 text-sm text-muted-foreground" role="status" data-chat-element-pending>Preparing visual…</div>
}

export function ChatElementBlock(props: ChatVisualProps) {
  // Terminal-only sources cannot progressively render; the server marks them.
  if (props.stream === 'pending') return <ChatVisualPending />
  return <Suspense fallback={<ChatVisualPending />}><ChatVisualRenderer {...props} /></Suspense>
}
