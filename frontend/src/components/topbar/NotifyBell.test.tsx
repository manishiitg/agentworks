// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TooltipProvider } from '../ui/tooltip'

const device = { id: 'device', kind: 'mac', label: 'This Mac', sound: true, added: '' }
const saved = { chats: 'mine', needs_me: true, devices: [device], vapid_public_key: '' }
const api = vi.hoisted(() => ({ load: vi.fn(), off: vi.fn(), on: vi.fn(), chats: vi.fn(), toast: vi.fn() }))
vi.mock('../../services/notifications', () => ({
  loadNotifySettings: api.load, notifyDeviceId: () => 'device', turnNotificationsOff: api.off,
  turnNotificationsOn: api.on, saveNotifyChats: api.chats, notifyErrorToast: () => ['Try again', 'error'],
}))
vi.mock('../../stores', () => ({ useChatStore: (select: (state: { addToast: typeof api.toast }) => unknown) => select({ addToast: api.toast }) }))
vi.mock('../../utils/sound', () => ({ playNotificationSound: vi.fn() }))
import NotifyBell from './NotifyBell'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const media = Object.getOwnPropertyDescriptor(window, 'matchMedia')
afterEach(() => {
  vi.useRealTimers(); vi.clearAllMocks()
  if (media) Object.defineProperty(window, 'matchMedia', media)
  else Reflect.deleteProperty(window, 'matchMedia')
})
async function mount(coarse = false) {
  vi.useFakeTimers()
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: (query: string) => ({ matches: query === '(hover: hover)' ? !coarse : coarse }) })
  api.load.mockResolvedValue(saved); api.off.mockResolvedValue([]); api.on.mockResolvedValue([device])
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<TooltipProvider><NotifyBell /></TooltipProvider>))
  const button = host.querySelector<HTMLButtonElement>('[data-testid="notify-bell"]')!
  return { host, button, wrapper: button.closest('div.relative')!, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('opens on hover with time to cross into the menu, while a click still toggles this device', async () => {
  const view = await mount()
  try {
    await act(async () => view.wrapper.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })))
    expect(view.host.querySelector('[role="menu"]')).not.toBeNull()
    expect(api.off).not.toHaveBeenCalled()
    await act(async () => view.wrapper.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, relatedTarget: document.body })))
    await act(async () => vi.advanceTimersByTime(150))
    const menu = view.host.querySelector('[role="menu"]')!
    expect(menu).not.toBeNull()
    await act(async () => menu.dispatchEvent(new MouseEvent('mouseover', { bubbles: true, relatedTarget: document.body })))
    await act(async () => vi.advanceTimersByTime(250))
    expect(view.host.querySelector('[role="menu"]')).not.toBeNull()
    await act(async () => menu.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, relatedTarget: document.body })))
    await act(async () => vi.advanceTimersByTime(250))
    expect(view.host.querySelector('[role="menu"]')).toBeNull()
    await act(async () => view.button.click())
    expect(api.off).toHaveBeenCalledOnce()
    expect(view.button.getAttribute('aria-pressed')).toBe('false')
  } finally { await view.cleanup() }
})

// PLAT-856-561: releasing a long-press must not also turn notifications off.
it('opens on touch long-press without toggling when the finger is released', async () => {
  const view = await mount(true)
  try {
    await act(async () => view.button.dispatchEvent(new Event('touchstart', { bubbles: true })))
    await act(async () => vi.advanceTimersByTime(550))
    expect(view.host.querySelector('[role="menu"]')).not.toBeNull()
    await act(async () => { view.button.dispatchEvent(new Event('touchend', { bubbles: true })); view.button.click() })
    expect(api.off).not.toHaveBeenCalled()
    expect(view.button.getAttribute('aria-pressed')).toBe('true')
    await act(async () => view.button.click())
    expect(api.off).toHaveBeenCalledOnce()
  } finally { await view.cleanup() }
})
