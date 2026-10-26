import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { OpaqueReportFrame } from '../workflow/reportWidgets/opaqueReportFrame'

interface HtmlRendererProps { content: string; onLinkClick?: (href: string) => boolean; autoHeight?: boolean; initialHeight?: number }

export function HtmlRenderer({ content, onLinkClick, autoHeight = false, initialHeight = 600 }: HtmlRendererProps) {
  const frame = useRef<HTMLIFrameElement>(null)
  const [height, setHeight] = useState(initialHeight)
  const latest = useRef({ autoHeight, onLinkClick })
  latest.current = { autoHeight, onLinkClick }
  useEffect(() => setHeight(initialHeight), [content, initialHeight])
  useLayoutEffect(() => {
    if (!frame.current || !content) return
    const document = new OpaqueReportFrame(frame.current, () => ({
      title: 'HTML Viewer', kind: 'html', dataApi: null, theme: window.document.documentElement.classList.contains('dark') ? 'dark' : 'light',
      onLink: latest.current.onLinkClick,
      onState: state => { if (latest.current.autoHeight && Number.isFinite(state.height) && state.height > 0) setHeight(state.height) },
    }))
    document.load(content)
    return () => document.dispose()
  }, [content])
  if (!content) return <div className="p-4 text-gray-500">Loading HTML...</div>
  return <div className={autoHeight ? 'w-full' : 'w-full h-full flex flex-col'}>
    <iframe ref={frame} className={autoHeight ? 'block w-full border-0' : 'flex-1 w-full border-0'} style={autoHeight ? { height: `${height}px` } : undefined}
      title="HTML Viewer" sandbox="allow-scripts" referrerPolicy="no-referrer" />
  </div>
}
