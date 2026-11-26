import { createContext, useContext, useMemo, useRef, useState } from 'react'
import { defineRegistry, JSONUIProvider, Renderer } from '@json-render/react'
import { chatVisualCatalog, workspaceCardImage, type ChatElement } from './chatElementSpec'
import { ChatChart, ChatDetails, ChatTable, ChatTimeline } from './ChatWidgets'
import { ChatImage } from './ChatImage'
import { ChatChecks, ChatFileChanges } from './ChatDeveloperWidgets'
import { ChatCards } from './ChatCards'
import { ChatDiagram } from './ChatDiagram'
import { ChatSequence, ChatSwimlane } from './ChatProcessDiagram'
import { ChatVisualPending, ChatVisualRepairContext, type ChatVisualProps } from './ChatElement'
import { compileChatVisual } from './chatVisualSpec'

const Presentation = createContext<Pick<ChatVisualProps, 'renderMarkdown' | 'disclosureKey'> | null>(null)

function Details({ spec }: { spec: Extract<ChatElement, { type: 'details' }> }) {
  return <ChatDetails spec={spec} {...useContext(Presentation)!} />
}
function Cards({ spec }: { spec: Extract<ChatElement, { type: 'cards' }> }) {
  return <ChatCards spec={spec} renderMarkdown={useContext(Presentation)!.renderMarkdown} />
}

function DeveloperWidget({ spec }: { spec: Extract<ChatElement, { type: 'file_changes' | 'checks' }> }) {
  const { renderMarkdown } = useContext(Presentation)!
  return spec.type === 'file_changes' ? <ChatFileChanges spec={spec} renderMarkdown={renderMarkdown} /> : <ChatChecks spec={spec} renderMarkdown={renderMarkdown} />
}

const { registry } = defineRegistry(chatVisualCatalog, {
  components: {
    file_changes: ({ props }) => <DeveloperWidget spec={{ ...props, type: 'file_changes' }} />,
    checks: ({ props }) => <DeveloperWidget spec={{ ...props, type: 'checks' }} />,
    image: ({ props }) => <figure className="not-prose my-4 max-w-full" data-chat-element="image"><ChatImage src={props.src} alt={props.caption} workspace={workspaceCardImage(props.src)} />{props.caption && <figcaption className="mt-2 text-xs text-muted-foreground">{props.caption}</figcaption>}</figure>,
    table: ({ props }) => <ChatTable spec={{ ...props, type: 'table' }} />,
    chart: ({ props }) => <ChatChart spec={{ ...props, type: 'chart' }} />,
    timeline: ({ props }) => <ChatTimeline spec={{ ...props, type: 'timeline' }} />,
    details: ({ props }) => <Details spec={{ ...props, type: 'details' }} />,
    cards: ({ props }) => <Cards spec={{ ...props, type: 'cards' }} />,
    diagram: ({ props }) => <ChatDiagram spec={{ ...props, type: 'diagram' }} />,
    swimlane: ({ props }) => <ChatSwimlane spec={{ ...props, type: 'swimlane' }} />,
    sequence: ({ props }) => <ChatSequence spec={{ ...props, type: 'sequence' }} />,
  },
})

export default function ChatVisualRenderer({ source, stream, renderMarkdown, disclosureKey }: ChatVisualProps) {
  const spec = useMemo(() => compileChatVisual(source, stream === 'progressive'), [source, stream])
  const presentation = useMemo(() => ({ renderMarkdown, disclosureKey }), [renderMarkdown, disclosureKey])
  if (spec) return <Presentation.Provider value={presentation}><JSONUIProvider registry={registry}><Renderer spec={spec} registry={registry} loading={!!stream} /></JSONUIProvider></Presentation.Provider>
  if (stream) return <ChatVisualPending />
  return <BrokenChatVisual source={source} />
}

function BrokenChatVisual({ source }: { source: string }) {
  const fix = useContext(ChatVisualRepairContext)
  const [status, setStatus] = useState<'idle' | 'sending' | 'sent' | 'failed'>('idle')
  const submitted = useRef(false)
  const send = async () => {
    if (!fix || submitted.current) return
    submitted.current = true
    setStatus('sending')
    try {
      if (await fix(source)) {
        setStatus('sent')
        return
      }
    } catch { /* The failed request can be retried from this card. */ }
    submitted.current = false
    setStatus('failed')
  }
  return <div className="not-prose my-4 min-w-0 max-w-xl rounded-xl border border-border bg-muted/20 px-4 py-3 text-foreground" data-chat-element-error>
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span className="break-words text-sm font-medium">Couldn’t display this visual</span>
      {fix && source.trim() && source.length <= 50000 && (
        <button type="button" disabled={status === 'sending' || status === 'sent'} onClick={() => void send()}
          className="rounded-md border border-border bg-primary px-3 py-1 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-60">
          {status === 'sending' ? 'Sending…' : status === 'sent' ? 'Request sent' : status === 'failed' ? 'Try again' : 'Fix visual'}
        </button>
      )}
    </div>
    <details className="mt-1">
      <summary className="cursor-pointer text-sm text-muted-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring">View source</summary>
      <pre className="mt-2 overflow-auto whitespace-pre-wrap break-words border-t border-border pt-2 text-xs">{source}</pre>
    </details>
    {status === 'failed' && <p className="mt-2 text-sm text-muted-foreground" role="status">Couldn’t send the request. Try again.</p>}
  </div>
}
