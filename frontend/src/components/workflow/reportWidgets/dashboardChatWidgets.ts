import type { ReportChatWidgetApi } from './reportEmbedContext'

export const CHAT_PANEL_ATTR = 'data-report-chat-panel'
export const CHAT_BUTTON_ATTR = 'data-report-chat-button'
export type ChatWidgetSlot = { key: number; left: number; top: number; width: number; height: number; visible: boolean; clipPath: string; palette?: string; mode?: ChatMode; vars?: Record<string, string> }
export type ChatMode = 'light' | 'dark'
export type ReportTheme = { vars: Record<string, string>; mode?: ChatMode }

export const CHAT_PALETTE_ATTR = 'data-report-chat-palette'
export const CHAT_VARS_ATTR = 'data-report-chat-vars'
export const CHAT_RADIUS_ATTR = 'data-report-chat-radius'
export const CHAT_FIT_ATTR = 'data-report-chat-fit'
export const CHAT_MODE_ATTR = 'data-report-chat-theme'
// The app's own report themes; each follows the app's light/dark mode.
const PALETTES = new Set(['anthropic', 'warm', 'cool', 'brand'])
// Only these theme tokens can be set, and only to a colour (hex or "H S% L%"); never URLs or arbitrary CSS.
const COLOR_TOKENS = new Set(['background', 'foreground', 'card', 'card-foreground', 'popover', 'popover-foreground', 'primary', 'primary-foreground', 'secondary', 'secondary-foreground', 'muted', 'muted-foreground', 'accent', 'accent-foreground', 'border', 'input', 'ring', 'destructive', 'destructive-foreground'])

function hexToTriplet(hex: string): string {
  const full = hex.length === 3 ? hex.split('').map(c => c + c).join('') : hex
  const r = parseInt(full.slice(0, 2), 16) / 255, g = parseInt(full.slice(2, 4), 16) / 255, b = parseInt(full.slice(4, 6), 16) / 255
  const max = Math.max(r, g, b), min = Math.min(r, g, b), l = (max + min) / 2
  const d = max - min
  let h = 0, sat = 0
  if (d) {
    sat = l > 0.5 ? d / (2 - max - min) : d / (max + min)
    h = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4
    h *= 60
  }
  return `${Math.round(h)} ${Math.round(sat * 100)}% ${Math.round(l * 100)}%`
}

function colorToken(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const text = value.trim()
  if (/^\d{1,3}(\.\d+)?\s+\d{1,3}(\.\d+)?%\s+\d{1,3}(\.\d+)?%$/.test(text)) return text
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(text)
  if (hex) return hexToTriplet(hex[1])
  const rgb = /^rgba?\(\s*(\d{1,3})\s*[,\s]\s*(\d{1,3})\s*[,\s]\s*(\d{1,3})\s*(?:[,/]\s*[\d.]+%?\s*)?\)$/i.exec(text)
  if (rgb && [rgb[1], rgb[2], rgb[3]].every(n => Number(n) <= 255)) return hexToTriplet([rgb[1], rgb[2], rgb[3]].map(n => Number(n).toString(16).padStart(2, '0')).join(''))
  return null
}

/** A dashboard can set its own look once, in its CSS, and the chat and the function
 * form follow it: --report-background, --report-foreground, --report-primary,
 * --report-border, --report-muted and so on (colours), and --report-radius. */
export function readReportTheme(get: (name: string) => string): ReportTheme {
  const vars: Record<string, string> = {}
  for (const name of COLOR_TOKENS) {
    const token = colorToken(get(`--report-${name}`))
    if (token) vars[`--${name}`] = token
  }
  const radius = get('--report-radius').trim()
  if (/^\d{1,2}(\.\d+)?(px|rem)$/.test(radius)) vars['--radius'] = radius
  const mode = get('--report-mode').trim().toLowerCase()
  return { vars, mode: mode === 'light' || mode === 'dark' ? mode : undefined }
}

/** A designer's styling for one chat panel: a built-in palette, a corner radius and a few colours. Invalid values are ignored. */
export function readChatStyle(el: Element): { palette?: string; vars?: Record<string, string>; fit?: 'viewport'; mode?: ChatMode } {
  const palette = (el.getAttribute(CHAT_PALETTE_ATTR) || '').trim().toLowerCase()
  const vars: Record<string, string> = {}
  const radius = (el.getAttribute(CHAT_RADIUS_ATTR) || '').trim()
  if (/^\d{1,2}(\.\d+)?(px|rem)$/.test(radius)) vars['--radius'] = radius
  const raw = el.getAttribute(CHAT_VARS_ATTR)
  if (raw) {
    try {
      const parsed = JSON.parse(raw)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        for (const [name, value] of Object.entries(parsed as Record<string, unknown>)) {
          const token = COLOR_TOKENS.has(name) ? colorToken(value) : null
          if (token) vars[`--${name}`] = token
        }
      }
    } catch { /* ignore malformed JSON */ }
  }
  const fit = (el.getAttribute(CHAT_FIT_ATTR) || '').trim().toLowerCase() === 'viewport' ? 'viewport' as const : undefined
  const modeAttr = (el.getAttribute(CHAT_MODE_ATTR) || '').trim().toLowerCase()
  return { palette: PALETTES.has(palette) ? palette : undefined, vars: Object.keys(vars).length ? vars : undefined, fit, mode: modeAttr === 'light' || modeAttr === 'dark' ? modeAttr : undefined }
}
export type DashboardChatDocumentOptions = {
  available: boolean; visible?: boolean; viewportHeight?: () => number; openConversation: () => Promise<unknown>
  changed: (slots: ChatWidgetSlot[], authored: boolean) => void; popup: (open: boolean) => void; error: (message: string) => void
  /** The dashboard's own --report-* theme, for native UI that is not a chat panel (the function form, the chat dialog). */
  themeChanged?: (theme: ReportTheme) => void
}

/** Only selectors and controls cross the report boundary; native chat stays in the host. */
export function installDashboardChatWidgets(doc: Document, options: DashboardChatDocumentOptions) {
  const win = doc.defaultView!
  // Suppress marked controls in old HTML and dynamically mounted React pages.
  // This changes presentation only; native chat permissions are unchanged.
  const visibility = doc.createElement('style')
  visibility.textContent = options.visible === false ? `[${CHAT_BUTTON_ATTR}], [${CHAT_PANEL_ATTR}] { display: none !important; }` : ''
  doc.head.append(visibility)
  let disposed = false, lastOpen = -Infinity, requested = false, nextKey = 0
  let pendingFrame = 0
  let lastSlots: ChatWidgetSlot[] | null = null, lastAuthored = false, lastTheme = ''
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
    const frameBox = { left: 0, top: 0, right: win.innerWidth, bottom: win.innerHeight }
    const hostBox = { left: 0, top: 0 }
    const sx = 1, sy = 1
    const slots: ChatWidgetSlot[] = []
    const rootStyle = win.getComputedStyle(doc.documentElement)
    const reportTheme = readReportTheme(name => rootStyle.getPropertyValue(name))
    const themeKey = JSON.stringify(reportTheme)
    if (themeKey !== lastTheme) { lastTheme = themeKey; options.themeChanged?.(reportTheme) }
    for (const el of keys.keys()) if (!panels.includes(el as HTMLElement)) { resize?.unobserve(el); observed.delete(el); keys.delete(el) }
    for (const el of options.visible === false ? [] : panels) {
      if (!keys.has(el)) keys.set(el, ++nextKey)
      if (!observed.has(el)) { observed.add(el); resize?.observe(el) }
      const box = el.getBoundingClientRect(), style = win.getComputedStyle(el)
      const left = frameBox.left + box.left * sx, top = frameBox.top + box.top * sy
      const width = box.width * sx
      let height = box.height * sy
      const chatStyle = readChatStyle(el)
      // Fit to window: keep the panel inside the visible area so its composer is never below the fold.
      if (chatStyle.fit === 'viewport') {
        const room = (options.viewportHeight?.() ?? win.innerHeight) - 8 - top
        if (room < height) height = Math.max(200, room)
      }
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
      const vars = { ...reportTheme.vars, ...chatStyle.vars }
      slots.push({ key: keys.get(el)!, left: left - hostBox.left, top: top - hostBox.top, width, height, visible, clipPath, palette: chatStyle.palette, mode: chatStyle.mode ?? reportTheme.mode, vars: Object.keys(vars).length ? vars : undefined })
    }
    const authored = panels.length + buttons.length > 0
    const unchanged = lastSlots && lastAuthored === authored && lastSlots.length === slots.length && slots.every((slot, i) => {
      const previous = lastSlots![i]
      return slot.key === previous.key && slot.left === previous.left && slot.top === previous.top && slot.width === previous.width && slot.height === previous.height && slot.visible === previous.visible && slot.clipPath === previous.clipPath && slot.palette === previous.palette && slot.mode === previous.mode && JSON.stringify(slot.vars) === JSON.stringify(previous.vars)
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
  mutation.observe(doc.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: [CHAT_PANEL_ATTR, CHAT_BUTTON_ATTR, CHAT_PALETTE_ATTR, CHAT_VARS_ATTR, CHAT_RADIUS_ATTR, CHAT_FIT_ATTR, CHAT_MODE_ATTR, 'data-report-chat-state', 'style', 'class', 'hidden'] })
  doc.addEventListener('click', click)
  win.addEventListener('scroll', scheduleMeasure, true); win.addEventListener('resize', scheduleMeasure)
  window.addEventListener('scroll', scheduleMeasure, true); window.addEventListener('resize', scheduleMeasure)
  measure()
  return { api, reveal, dispose: () => {
    disposed = true; visibility.remove(); mutation.disconnect(); resize?.disconnect()
    win.cancelAnimationFrame(pendingFrame)
    doc.removeEventListener('click', click)
    win.removeEventListener('scroll', scheduleMeasure, true); win.removeEventListener('resize', scheduleMeasure)
    window.removeEventListener('scroll', scheduleMeasure, true); window.removeEventListener('resize', scheduleMeasure)
    for (const [button, wasDisabled] of disabled) button.disabled = wasDisabled
    keys.clear(); disabled.clear()
  } }
}
