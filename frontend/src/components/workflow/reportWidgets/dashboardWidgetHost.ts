import { readReportTheme, type ChatWidgetSlot, type ReportTheme, type DashboardChatDocumentOptions } from './dashboardChatWidgets'
import type { ActionRequest } from './reportActionButtons'
import type { ReportChatWidgetApi } from './reportEmbedContext'
import type { ReportDocumentBridge } from './reportDocumentBridge'

type Options = Omit<DashboardChatDocumentOptions, 'viewportHeight'> & {
  container: () => HTMLElement | null; functionsAvailable: boolean; openAction: (action: ActionRequest) => void
}
const theme = (value: unknown): ReportTheme => {
  const incoming = value && typeof value === 'object' ? value as ReportTheme : { vars: {} }
  const clean = readReportTheme(name => String(incoming.vars?.[name.replace('--report-', '--')] ?? ''))
  return { ...clean, mode: incoming.mode === 'light' || incoming.mode === 'dark' ? incoming.mode : undefined }
}

/** The child measures its own DOM; native overlays remain clipped to that exact frame. */
export function installDashboardWidgetHost(frame: HTMLIFrameElement, bridge: ReportDocumentBridge, options: Options) {
  let raw: ChatWidgetSlot[] = [], authored = false, blocked: string[] = []
  const measure = () => {
    const element = options.container()
    if (!element) return
    const bounds = frame.getBoundingClientRect(), container = element.getBoundingClientRect()
    const sx = bounds.width / (frame.clientWidth || bounds.width || 1), sy = bounds.height / (frame.clientHeight || bounds.height || 1)
    const slots: ChatWidgetSlot[] = []
    for (const slot of raw) {
      if (!slot || typeof slot !== 'object') continue
      if (!Number.isFinite(slot.key) || ![slot.left, slot.top, slot.width, slot.height].every(n => Number.isFinite(n) && Math.abs(n) <= 1_000_000) || slot.width <= 0 || slot.height <= 0) continue
      const inset = /^inset\(([\d.]+)px ([\d.]+)px ([\d.]+)px ([\d.]+)px\)$/.exec(slot.clipPath || '')
      if (!inset) continue
      const [it, ir, ib, il] = inset.slice(1).map(Number)
      if (![it, ir, ib, il].every(n => Number.isFinite(n) && n <= 1_000_000)) continue
      const left = bounds.left + slot.left * sx, top = bounds.top + slot.top * sy
      const width = slot.width * sx, height = slot.height * sy
      // Invalid CSS numbers must never remove the clip on a native overlay.
      if (![left, top, width, height, left + width, top + height].every(Number.isFinite)) continue
      let clipLeft = Math.max(left + il * sx, bounds.left, 0), clipTop = Math.max(top + it * sy, bounds.top, 0)
      let clipRight = Math.min(left + width - ir * sx, bounds.right, window.innerWidth), clipBottom = Math.min(top + height - ib * sy, bounds.bottom, window.innerHeight)
      for (let parent = frame.parentElement; parent && parent !== document.body; parent = parent.parentElement) {
        const css = getComputedStyle(parent), rect = parent.getBoundingClientRect()
        if (/hidden|clip|auto|scroll/.test(css.overflowX || css.overflow)) { clipLeft = Math.max(clipLeft, rect.left); clipRight = Math.min(clipRight, rect.right) }
        if (/hidden|clip|auto|scroll/.test(css.overflowY || css.overflow)) { clipTop = Math.max(clipTop, rect.top); clipBottom = Math.min(clipBottom, rect.bottom) }
      }
      const cleaned = theme({ vars: slot.vars, mode: slot.mode })
      slots.push({ key: slot.key, left: left - container.left, top: top - container.top, width, height,
        visible: Boolean(slot.visible) && clipRight > clipLeft && clipBottom > clipTop,
        clipPath: `inset(${Math.max(0, clipTop - top)}px ${Math.max(0, left + width - clipRight)}px ${Math.max(0, top + height - clipBottom)}px ${Math.max(0, clipLeft - left)}px)`,
        vars: cleaned.vars, mode: cleaned.mode, palette: typeof slot.palette === 'string' && /^[a-z][a-z0-9-]{0,48}$/.test(slot.palette) ? slot.palette : undefined })
    }
    options.changed(slots, authored)
  }
  const removals = [
    bridge.handle('chat.open', async () => {
      if (!options.available) throw new Error('Chat is unavailable.')
      if (!raw.length && document.activeElement?.matches('input, textarea, [contenteditable="true"]')) throw new Error('Finish editing this field before opening chat.')
      await options.openConversation()
    }),
    bridge.handle('chat.popup', open => {
      const editing = document.activeElement?.matches('input, textarea, [contenteditable="true"]')
      if (open === true && editing) throw new Error('Finish editing this field before opening chat.')
      options.popup(open === true)
    }),
    bridge.handle('chat.error', message => { if (typeof message === 'string') options.error(message.slice(0, 400)) }),
    bridge.handle('widgets.layout', (value, present) => { if (Array.isArray(value)) { raw = value as ChatWidgetSlot[]; authored = present === true; measure() } }),
    bridge.handle('widgets.theme', value => options.themeChanged?.(theme(value))),
    bridge.handle('action.open', value => {
      if (!value || typeof value !== 'object') throw new Error('Choose a published action.')
      if (!options.functionsAvailable) throw new Error('Actions are unavailable.');
      const action = value as ActionRequest
      if (typeof action.name !== 'string' || !action.name.trim() || action.name.length > 128) throw new Error('Choose a published action.')
      options.openAction({ name: action.name, label: typeof action.label === 'string' ? action.label.slice(0, 120) : action.name,
        defaults: action.defaults && typeof action.defaults === 'object' && !Array.isArray(action.defaults) ? action.defaults : undefined })
    }),
  ]
  const api: ReportChatWidgetApi = {
    available: options.available,
    mount: async selector => { await bridge.request('widgets.mount', [selector]) },
    open: async selector => { await bridge.request('widgets.open', [selector]) },
    close: selector => bridge.notify('widgets.close', [selector]),
  }
  window.addEventListener('scroll', measure, true); window.addEventListener('resize', measure)
  const resize = new ResizeObserver(measure)
  resize.observe(frame)
  return { api,
    context: () => ({ chatAvailable: options.available, chatVisible: options.visible !== false, functionsAvailable: options.functionsAvailable, blockedFunctions: blocked }),
    setBlocked: (names: string[]) => { blocked = names; bridge.notify('actions.blocked', [names]) },
    reveal: (selector?: string) => {
      // Native question submission deliberately opens its result while that
      // question's dialog still has focus. Child popup requests keep the guard.
      if (!authored && selector === undefined) options.popup(true)
      else bridge.notify('widgets.reveal', [selector])
    },
    dispose: () => { removals.forEach(remove => remove()); resize.disconnect(); window.removeEventListener('scroll', measure, true); window.removeEventListener('resize', measure) },
  }
}
