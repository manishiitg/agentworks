// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { installDashboardChatWidgets, readChatStyle, readReportTheme, type ChatWidgetSlot } from './dashboardChatWidgets'

const dispose: (() => void)[] = []
afterEach(() => { dispose.splice(0).forEach(fn => fn()); document.body.replaceChildren(); vi.restoreAllMocks() })
function fixture(available = true) {
  const host = document.createElement('div'), frame = document.createElement('iframe')
  host.append(frame); document.body.append(host)
  const doc = frame.contentDocument!
  doc.body.innerHTML = '<section id="mine" data-report-chat-panel></section><button data-report-chat-button data-report-chat-target="#mine">Help me</button>'
  const slot = doc.querySelector<HTMLElement>('#mine')!
  let box = { left: 210, top: 30, width: 280, height: 360 }
  vi.spyOn(host, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0 } as DOMRect)
  vi.spyOn(frame, 'getBoundingClientRect').mockReturnValue({ left: 5, top: 10, right: 605, bottom: 510, width: 600, height: 500 } as DOMRect)
  vi.spyOn(slot, 'getBoundingClientRect').mockImplementation(() => ({ ...box, right: box.left + box.width, bottom: box.top + box.height } as DOMRect))
  const openConversation = vi.fn().mockResolvedValue('native-tab'), popup = vi.fn(), error = vi.fn()
  let positions: ChatWidgetSlot[] = []
  const changed = vi.fn((next: ChatWidgetSlot[]) => { positions = next })
  const binding = installDashboardChatWidgets(frame, { available, container: host, openConversation, popup, error, changed })
  dispose.push(binding.dispose)
  return { ...binding, doc, slot, openConversation, popup, error, changed, positions: () => positions, move: async (value: typeof box) => { box = value; doc.defaultView!.dispatchEvent(new Event('resize')); await new Promise<void>(resolve => doc.defaultView!.requestAnimationFrame(() => resolve())) } }
}

it('uses designer slot geometry, only exposes four controls, tracks visibility and cleans old-document callbacks', async () => {
  const f = fixture()
  expect(Object.keys(f.api).sort()).toEqual(['available', 'close', 'mount', 'open'])
  expect(Object.isFrozen(f.api)).toBe(true)
  expect(f.positions()[0]).toMatchObject({ left: 215, top: 40, width: 280, height: 360, visible: true })
  expect(f.openConversation).toHaveBeenCalledTimes(1) // opens history; no send API exists.
  await f.move({ left: 30, top: 15, width: 450, height: 250 })
  expect(f.positions()[0]).toMatchObject({ left: 35, top: 25, width: 450, height: 250 })
  const count = f.changed.mock.calls.length
  await f.move({ left: 30, top: 15, width: 450, height: 250 })
  expect(f.changed).toHaveBeenCalledTimes(count)
  await f.move({ left: 30, top: 600, width: 450, height: 250 })
  expect(f.positions()[0].visible).toBe(false)
  f.api.close('#mine'); expect(f.positions()[0].visible).toBe(false)
  await f.api.open('#mine')
  expect(f.slot.getAttribute('data-report-chat-state')).toBe('open')
  expect(f.popup).not.toHaveBeenCalled()
  await expect(f.api.open('#mine')).rejects.toThrow('already opening')
  f.dispose()
  await expect(f.api.mount('#mine')).rejects.toThrow('document changed')
})

it('keeps unavailable widgets disabled, scopes selectors, and refuses a script popup while someone types', async () => {
  const unavailable = fixture(false)
  expect(unavailable.doc.querySelector<HTMLButtonElement>('button')!.disabled).toBe(true)
  expect(unavailable.openConversation).not.toHaveBeenCalled()
  await expect(unavailable.api.open('#mine')).rejects.toThrow('unavailable')
  const f = fixture()
  await expect(f.api.mount('body')).rejects.toThrow('section or div')
  await expect(f.api.open('#another-project')).rejects.toThrow('container')
  f.slot.remove()
  const input = document.createElement('input'); document.body.append(input); input.focus()
  await expect(f.api.open()).rejects.toThrow('Finish editing')
  expect(f.popup).not.toHaveBeenCalled()
})

it('reads a designer chat style: a built-in palette, a radius and colours, and ignores anything else', () => {
  const el = document.createElement('section')
  el.setAttribute('data-report-chat-palette', 'Anthropic')
  el.setAttribute('data-report-chat-radius', '6px')
  el.setAttribute('data-report-chat-vars', JSON.stringify({ background: '#ffffff', primary: '#1f883d', border: '208 14% 82%', 'font-family': 'x', foreground: 'url(javascript:1)', unknown: '#000' }))
  expect(readChatStyle(el)).toEqual({ palette: 'anthropic', vars: { '--radius': '6px', '--background': '0 0% 100%', '--primary': '137 63% 33%', '--border': '208 14% 82%' } })
  const bad = document.createElement('section')
  bad.setAttribute('data-report-chat-palette', 'x'); bad.setAttribute('data-report-chat-radius', '99999px'); bad.setAttribute('data-report-chat-vars', '{oops')
  expect(readChatStyle(bad)).toEqual({ palette: undefined, vars: undefined })
})

it("reads the dashboard's own --report-* theme and the fit-to-window option", () => {
  const css: Record<string, string> = { '--report-background': '#fff', '--report-primary': 'rgb(31, 136, 61)', '--report-border': '208 14% 82%', '--report-radius': '8px', '--report-foreground': 'url(x)', '--report-unknown': '#000' }
  css['--report-mode'] = 'light'
  expect(readReportTheme(name => css[name] ?? '')).toEqual({ vars: { '--background': '0 0% 100%', '--primary': '137 63% 33%', '--border': '208 14% 82%', '--radius': '8px' }, mode: 'light' })
  const el = document.createElement('section')
  el.setAttribute('data-report-chat-fit', 'viewport'); el.setAttribute('data-report-chat-theme', 'DARK')
  expect(readChatStyle(el)).toMatchObject({ fit: 'viewport', mode: 'dark' })
})
