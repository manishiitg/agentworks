import { memo, useEffect, useLayoutEffect, useRef } from 'react'
import { useReportDataApi, useReportChatWidgetFactory } from './reportEmbedContext'
import { OpaqueReportFrame, type ReportDocumentSnapshot, type ReportFrameOptions } from './opaqueReportFrame'
import type { ReportTabSelection } from './reportTabSelection'
import type { ReportContentMenu } from './reportContentQuestion'

const APP_SHORTCUTS = new Set(['1', '2', '3', '6', '7', 'k', 'n'])
function forwardShortcut(key: KeyboardEvent) {
  if (!(key.ctrlKey || key.metaKey) || key.altKey || !APP_SHORTCUTS.has(key.key.toLowerCase())) return false
  window.dispatchEvent(key)
  return true
}

/** Authored HTML owns its rendering in an opaque document. Only report values and named controls cross. */
function HtmlReportFrameComponent({ html, title, className, autoHeight = false, refreshToken = 0, focusTarget, onContentContextMenu }: {
  html: string; title: string; className: string; autoHeight?: boolean; refreshToken?: number
  focusTarget?: { value: string; token: number }; onContentContextMenu?: (menu: ReportContentMenu) => void
}) {
  const dataApi = useReportDataApi()
  const factory = useReportChatWidgetFactory()
  const frame = useRef<HTMLIFrameElement>(null)
  const document = useRef<OpaqueReportFrame | null>(null)
  const binding = useRef<ReturnType<NonNullable<typeof factory>> | null>(null)
  const savedSelection = useRef<ReportTabSelection | undefined>(undefined)
  const applied = useRef({ dataApi, factory, refreshToken, focusTarget })
  const latest = useRef({ dataApi, factory, title, autoHeight, refreshToken, focusTarget, onContentContextMenu })
  latest.current = { dataApi, factory, title, autoHeight, refreshToken, focusTarget, onContentContextMenu }

  const resize = (state: ReportDocumentSnapshot) => {
    const element = frame.current
    if (!element || !latest.current.autoHeight) return
    let height = state.height
    if (state.fitViewport) {
      const bounds = element.closest('[data-report-viewport]')?.getBoundingClientRect()
      height = Math.min(window.innerHeight, bounds?.bottom ?? window.innerHeight) - Math.max(element.getBoundingClientRect().top, bounds?.top ?? 0)
    }
    if (Number.isFinite(height) && height > 0) {
      const next = Math.ceil(height) + 'px'
      if (element.style.height !== next) element.style.height = next
    }
  }
  const options = (): ReportFrameOptions => ({
    title: latest.current.title, dataApi: latest.current.dataApi,
    theme: window.document.documentElement.classList.contains('dark') || window.document.documentElement.classList.contains('dark-plus') ? 'dark' : 'light',
    tokenSource: frame.current, widgets: () => binding.current?.context() ?? {},
    focus: latest.current.focusTarget, selection: savedSelection.current,
    onState: resize, onContentMenu: latest.current.onContentContextMenu, onShortcut: forwardShortcut,
  })

  useLayoutEffect(() => {
    const element = frame.current
    if (!element) return
    const next = new OpaqueReportFrame(element, options)
    document.current = next
    binding.current = latest.current.factory?.(element, next.bridge) ?? null
    applied.current = { dataApi: latest.current.dataApi, factory: latest.current.factory, refreshToken: latest.current.refreshToken, focusTarget: latest.current.focusTarget }
    next.load(html)
    return () => {
      savedSelection.current = next.state.selection
      binding.current?.dispose(); binding.current = null
      next.dispose()
      if (document.current === next) document.current = null
    }
  }, [html])

  useEffect(() => {
    const next = document.current, element = frame.current
    if (!next || !element) return
    const previous = applied.current
    if (previous.factory !== factory) { binding.current?.dispose(); binding.current = factory?.(element, next.bridge) ?? null }
    const refresh = previous.dataApi !== dataApi || previous.refreshToken !== refreshToken
    if (refresh || previous.factory !== factory || previous.focusTarget?.token !== focusTarget?.token) next.update(refresh)
    applied.current = { dataApi, factory, refreshToken, focusTarget }
  }, [dataApi, factory, refreshToken, focusTarget?.token])

  useEffect(() => {
    const changed = () => { const current = document.current; if (current) { resize(current.state); current.update(false) } }
    const theme = new MutationObserver(changed)
    theme.observe(window.document.documentElement, { attributes: true, attributeFilter: ['class'] })
    const viewport = new ResizeObserver(changed)
    if (frame.current) viewport.observe(frame.current)
    window.addEventListener('resize', changed)
    return () => { theme.disconnect(); viewport.disconnect(); window.removeEventListener('resize', changed) }
  }, [])
  return <iframe ref={frame} title={title} sandbox="allow-scripts" referrerPolicy="no-referrer" className={className} />
}
export const HtmlReportFrame = memo(HtmlReportFrameComponent)
