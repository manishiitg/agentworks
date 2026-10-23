import { useEffect, useMemo, useRef, useState } from 'react'
import { getApiBaseUrl } from '../../services/api'
import { chatVisualDocument } from './chatVisualDocument'

export function ChatVisual({ html }: { html: string }) {
  const [channel] = useState(() => crypto.randomUUID())
  const document = useMemo(() => chatVisualDocument(html, channel), [html, channel])
  const [failed, setFailed] = useState(false)
  const [height, setHeight] = useState(320)
  const frame = useRef<HTMLIFrameElement>(null)
  const sent = useRef(false)
  useEffect(() => {
    sent.current = false
    setFailed(false)
    setHeight(320)
    const timer = window.setTimeout(() => setFailed(true), 10000)
    const receive = (event: MessageEvent) => {
      if (event.source !== frame.current?.contentWindow || event.data?.channel !== channel) return
      if (event.data.kind === 'ready') window.clearTimeout(timer)
      if (event.data.kind === 'error') { window.clearTimeout(timer); setFailed(true) }
      if (event.data.kind === 'size' && Number.isFinite(event.data.height)) setHeight(Math.max(120, Math.min(1200, event.data.height)))
    }
    window.addEventListener('message', receive)
    return () => { window.clearTimeout(timer); window.removeEventListener('message', receive) }
  }, [html, channel])
  const source = <pre className="my-0 max-h-[36rem] overflow-auto whitespace-pre-wrap p-3 text-xs leading-5"><code>{html}</code></pre>
  if (!document || failed) return <div data-chat-visual="source" className="my-3 overflow-hidden rounded-lg border border-border bg-muted/30">{failed && <p className="px-3 pt-3 text-xs text-muted-foreground">This visual could not be displayed. Its code is shown below.</p>}{source}</div>
  return <figure data-chat-visual="preview" className="my-3 min-w-0 overflow-hidden rounded-xl border border-border bg-card">
    <iframe key={html} ref={frame} title={document.title} sandbox="allow-scripts" referrerPolicy="no-referrer" className="block w-full border-0" style={{ height }} src={`${getApiBaseUrl()}/api/mcp-apps/frame`} onError={() => setFailed(true)} onLoad={() => {
      if (sent.current) return
      sent.current = true
      frame.current?.contentWindow?.postMessage({ kind: 'agentworks-dashboard-document', html: document.html }, '*')
    }} />
    <details className="border-t border-border"><summary className="cursor-pointer px-3 py-2 text-xs text-muted-foreground">View code</summary>{source}</details>
  </figure>
}
