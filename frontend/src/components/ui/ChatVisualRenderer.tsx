import { createContext, useContext, useMemo } from 'react'
import { defineRegistry, JSONUIProvider, Renderer } from '@json-render/react'
import { chatVisualCatalog, type ChatElement } from './chatElementSpec'
import { ChatChart, ChatDetails, ChatTable, ChatTimeline } from './ChatWidgets'
import { ChatCards } from './ChatCards'
import { ChatDiagram } from './ChatDiagram'
import { ChatSequence, ChatSwimlane } from './ChatProcessDiagram'
import { ChatVisualPending, type ChatVisualProps } from './ChatElement'
import { compileChatVisual } from './chatVisualSpec'

const Presentation = createContext<Pick<ChatVisualProps, 'renderMarkdown' | 'disclosureKey'> | null>(null)

function Details({ spec }: { spec: Extract<ChatElement, { type: 'details' }> }) {
  return <ChatDetails spec={spec} {...useContext(Presentation)!} />
}
function Cards({ spec }: { spec: Extract<ChatElement, { type: 'cards' }> }) {
  return <ChatCards spec={spec} renderMarkdown={useContext(Presentation)!.renderMarkdown} />
}

const { registry } = defineRegistry(chatVisualCatalog, {
  components: {
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
  return <details className="not-prose my-4 min-w-0 max-w-xl rounded-xl border border-border bg-muted/20 text-foreground" data-chat-element-error>
    <summary className="cursor-pointer break-words px-4 py-3 text-sm font-medium marker:text-muted-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring">Couldn’t display this visual · View source</summary>
    <pre className="overflow-auto whitespace-pre-wrap break-words border-t border-border px-4 py-3 text-xs">{source}</pre>
  </details>
}
