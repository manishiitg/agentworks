import type { ReportChatWidgetApi } from './reportEmbedContext'

export const CHAT_PANEL_ATTR = 'data-report-chat-panel'
export const CHAT_BUTTON_ATTR = 'data-report-chat-button'
export type ChatWidgetSlot = { key: number; left: number; top: number; width: number; height: number; visible: boolean; clipPath: string }
type Options = {
  available: boolean; container: HTMLElement; openConversation: () => Promise<unknown>
  changed: (slots: ChatWidgetSlot[], authored: boolean) => void; popup: (open: boolean) => void; error: (message: string) => void
}

/** Only selectors and controls cross the report boundary; native chat stays in the host. */
export function installDashboardChatWidgets(frame: HTMLIFrameElement, options: Options) {
  const doc = frame.contentDocument!
  const win = doc.defaultView!
  let disposed = false, lastOpen = -Infinity, requested = false, nextKey = 0
  let pendingFrame = 0
  let lastSlots: ChatWidgetSlot[] | null = null, lastAuthored = false
  const scheduleMeasure = () => {
    if (disposed || pendingFrame) return
    pendingFrame = win.requestAnimationFrame(() => { pendingFrame = 0; measure() })
  }
  const keys = new Map<Element, number>()
  const disabled = new Map<HTMLButtonElement, boolean>()
  const observed = new Set<Element>()
  const resize = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(scheduleMeasure)
  const target = (selector: unknown): HTMLElement => {
    if (typeof selector !== 'string' || !selector.trim()) throw new Error('Chat needs a dashboard container selector.')
    const el = doc.querySelector<HTMLElement>(selector)
    if (!el || !['DIV', 'SECTION', 'ASIDE', 'ARTICLE', 'MAIN'].includes(el.tagName)) throw new Error('Chat container must be a section or div in this dashboard.')
    return el
  }
  const measure = () => {
    if (disposed) return
    const panels = [...doc.querySelectorAll<HTMLElement>(`[${CHAT_PANEL_ATTR}]`)]
    const buttons = [...doc.querySelectorAll<HTMLButtonElement>(`button[${CHAT_BUTTON_ATTR}]`)]
    for (const button of buttons) {
      if (!disabled.has(button)) disabled.set(button, button.disabled)
      if (!options.available) button.disabled = true
    }
    const frameBox = frame.getBoundingClientRect(), hostBox = options.container.getBoundingClientRect()
    const sx = frame.clientWidth > 0 ? frameBox.width / frame.clientWidth : 1
    const sy = frame.clientHeight > 0 ? frameBox.height / frame.clientHeight : 1
    const slots: ChatWidgetSlot[] = []
    if (!observed.has(frame)) { observed.add(frame); resize?.observe(frame) }
    for (const el of keys.keys()) if (!panels.includes(el as HTMLElement)) { resize?.unobserve(el); observed.delete(el); keys.delete(el) }
    for (const el of panels) {
      if (!keys.has(el)) keys.set(el, ++nextKey)
      if (!observed.has(el)) { observed.add(el); resize?.observe(el) }
      const box = el.getBoundingClientRect(), style = win.getComputedStyle(el)
      const left = frameBox.left + box.left * sx, top = frameBox.top + box.top * sy
      const width = box.width * sx, height = box.height * sy
      let clipLeft = Math.max(left, frameBox.left, 0), clipTop = Math.max(top, frameBox.top, 0)
      let clipRight = Math.min(left + width, frameBox.right, window.innerWidth), clipBottom = Math.min(top + height, frameBox.bottom, window.innerHeight)
      // Native UI must obey the author's nested scrolling/clipping containers too.
      for (let parent = el.parentElement; parent && parent !== doc.body; parent = parent.parentElement) {
        const css = win.getComputedStyle(parent), bounds = parent.getBoundingClientRect()
        if (/hidden|clip|auto|scroll/.test(css.overflowX || css.overflow)) {
          clipLeft = Math.max(clipLeft, frameBox.left + bounds.left * sx)
          clipRight = Math.min(clipRight, frameBox.left + bounds.right * sx)
        }
        if (/hidden|clip|auto|scroll/.test(css.overflowY || css.overflow)) {
          clipTop = Math.max(clipTop, frameBox.top + bounds.top * sy)
          clipBottom = Math.min(clipBottom, frameBox.top + bounds.bottom * sy)
        }
      }
      const visible = el.getAttribute('data-report-chat-state') !== 'closed' && !el.hidden && style.display !== 'none' && style.visibility !== 'hidden' && clipRight > clipLeft && clipBottom > clipTop
      const clipPath = `inset(${Math.max(0, clipTop - top)}px ${Math.max(0, left + width - clipRight)}px ${Math.max(0, top + height - clipBottom)}px ${Math.max(0, clipLeft - left)}px)`
      slots.push({ key: keys.get(el)!, left: left - hostBox.left, top: top - hostBox.top, width, height, visible, clipPath })
    }
    const authored = panels.length + buttons.length > 0
    const unchanged = lastSlots && lastAuthored === authored && lastSlots.length === slots.length && slots.every((slot, i) => {
      const previous = lastSlots![i]
      return slot.key === previous.key && slot.left === previous.left && slot.top === previous.top && slot.width === previous.width && slot.height === previous.height && slot.visible === previous.visible && slot.clipPath === previous.clipPath
    })
    if (!unchanged) { lastSlots = slots; lastAuthored = authored; options.changed(slots, authored) }
    if (slots.some(slot => slot.visible) && options.available && !requested) {
      requested = true
      void options.openConversation().catch(reason => { if (!disposed) options.error(reason instanceof Error ? reason.message : 'Could not open chat.') })
    }
  }
  const reveal = (selector?: string) => {
    if (disposed) return
    const el = selector === undefined ? doc.querySelector<HTMLElement>(`[${CHAT_PANEL_ATTR}]`) : target(selector)
    if (el) { el.setAttribute(CHAT_PANEL_ATTR, ''); el.setAttribute('data-report-chat-state', 'open'); measure() }
    else options.popup(true)
  }
  const openImpl: ReportChatWidgetApi['open'] = async selector => {
    if (disposed) throw new Error('This dashboard document changed.')
    if (!options.available) throw new Error('Chat is unavailable for this dashboard.')
    const el = selector === undefined ? doc.querySelector<HTMLElement>(`[${CHAT_PANEL_ATTR}]`) : target(selector)
    const typing = (el: Element | null) => Boolean(el?.matches('input, textarea') || (el as HTMLElement | null)?.isContentEditable)
    if (!el && (typing(document.activeElement) || typing(doc.activeElement))) throw new Error('Finish editing this field before opening chat.')
    if (Date.now() - lastOpen < 600) throw new Error('Chat is already opening. Try again in a moment.')
    lastOpen = Date.now()
    await options.openConversation()
    if (disposed) throw new Error('This dashboard document changed.')
    reveal(selector)
  }
  const api: ReportChatWidgetApi = Object.freeze({
    available: options.available,
    mount: async (selector: string) => {
      if (disposed) throw new Error('This dashboard document changed.')
      const el = target(selector)
      el.setAttribute(CHAT_PANEL_ATTR, '')
      measure()
    },
    open: openImpl,
    close: (selector?: string) => {
      if (disposed) return
      if (selector === undefined) options.popup(false)
      const el = selector === undefined ? doc.querySelector<HTMLElement>(`[${CHAT_PANEL_ATTR}]`) : target(selector)
      if (el) { el.setAttribute('data-report-chat-state', 'closed'); measure() }
    },
  })
  const click = (event: Event) => {
    const button = (event.target as Element | null)?.closest<HTMLButtonElement>(`button[${CHAT_BUTTON_ATTR}]`)
    if (!button || button.disabled) return
    event.preventDefault()
    void openImpl(button.getAttribute('data-report-chat-target') || undefined).catch(reason => { if (!disposed) options.error(reason instanceof Error ? reason.message : 'Could not open chat.') })
  }
  const mutation = new MutationObserver(scheduleMeasure)
  mutation.observe(doc.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: [CHAT_PANEL_ATTR, CHAT_BUTTON_ATTR, 'data-report-chat-state', 'style', 'class', 'hidden'] })
  doc.addEventListener('click', click)
  win.addEventListener('scroll', scheduleMeasure, true); win.addEventListener('resize', scheduleMeasure)
  window.addEventListener('scroll', scheduleMeasure, true); window.addEventListener('resize', scheduleMeasure)
  measure()
  return { api, reveal, dispose: () => {
    disposed = true; mutation.disconnect(); resize?.disconnect()
    win.cancelAnimationFrame(pendingFrame)
    doc.removeEventListener('click', click)
    win.removeEventListener('scroll', scheduleMeasure, true); win.removeEventListener('resize', scheduleMeasure)
    window.removeEventListener('scroll', scheduleMeasure, true); window.removeEventListener('resize', scheduleMeasure)
    for (const [button, wasDisabled] of disabled) button.disabled = wasDisabled
    keys.clear(); disabled.clear()
  } }
}
