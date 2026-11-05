import { useEffect, useRef, useState } from 'react'
import { Bell, BellOff, Check } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { iconButtonClass } from '../ui/IconPopover'
import { useChatStore } from '../../stores'
import { playNotificationSound } from '../../utils/sound'
import {
  loadNotifySettings, notifyDeviceId, saveNotifyChats, turnNotificationsOff, turnNotificationsOn,
  type NotifyChats, type NotifySettings,
} from '../../services/notifications'

const isMacApp = () => !!window.electronAPI?.getDesktopConnection

/**
 * The notifications bell (owner, 5 Nov, approved mockup): one click turns notifications on or off for this device;
 * right-click (or long-press) chooses which chats and, on a computer, the short sound. The server decides every send.
 */
export default function NotifyBell() {
  const [settings, setSettings] = useState<NotifySettings | null>(null)
  const [busy, setBusy] = useState(false)
  const [menu, setMenu] = useState(false)
  const wrapper = useRef<HTMLDivElement>(null)
  const press = useRef<number | null>(null)
  const addToast = useChatStore(state => state.addToast)

  useEffect(() => {
    let cancelled = false
    loadNotifySettings().then(value => { if (!cancelled) setSettings(value) }).catch(() => { /* the bell stays off */ })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!menu) return
    const close = (event: MouseEvent | KeyboardEvent) => {
      if (event instanceof KeyboardEvent ? event.key === 'Escape' : !wrapper.current?.contains(event.target as Node)) setMenu(false)
    }
    window.addEventListener('mousedown', close)
    window.addEventListener('keydown', close)
    return () => { window.removeEventListener('mousedown', close); window.removeEventListener('keydown', close) }
  }, [menu])

  const device = settings?.devices.find(item => item.id === notifyDeviceId())
  const on = !!device

  const run = async (change: () => Promise<void>) => {
    if (busy) return
    setBusy(true)
    try { await change() } catch (error) { addToast(error instanceof Error ? error.message : 'Notifications could not be changed. Try again.', 'error') }
    finally { setBusy(false) }
  }

  const toggle = () => run(async () => {
    if (!settings) return
    const devices = on ? await turnNotificationsOff() : await turnNotificationsOn(settings, isMacApp())
    setSettings({ ...settings, devices })
  })

  const setSound = (sound: boolean) => run(async () => {
    if (!settings || !on) return
    setSettings({ ...settings, devices: await turnNotificationsOn(settings, sound) })
    if (sound) playNotificationSound()
  })

  const setChats = (chats: NotifyChats) => run(async () => {
    setSettings(await saveNotifyChats(chats))
  })

  const label = on ? 'Notifications on for this device' : 'Notifications off · click to turn on'
  const choice = (selected: boolean, text: string, onClick: () => void) => (
    <button type="button" role="menuitemradio" aria-checked={selected} onClick={onClick}
      className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-gray-100 dark:hover:bg-gray-700">
      <Check className={`h-3.5 w-3.5 ${selected ? '' : 'invisible'}`} />{text}
    </button>
  )

  return <div ref={wrapper} className="relative">
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button" aria-label={label} aria-pressed={on} disabled={busy || !settings} data-testid="notify-bell"
          className={`${iconButtonClass} ${on ? 'text-blue-600 dark:text-blue-400' : 'opacity-70'}`}
          onClick={toggle}
          onContextMenu={event => { event.preventDefault(); setMenu(true) }}
          onTouchStart={() => { press.current = window.setTimeout(() => setMenu(true), 550) }}
          onTouchEnd={() => { if (press.current) window.clearTimeout(press.current) }}>
          {on ? <Bell className="h-4 w-4" fill="currentColor" /> : <BellOff className="h-4 w-4" />}
        </button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
    {menu && settings && <div role="menu" className="absolute right-0 top-full z-50 mt-1 w-56 rounded-md border border-gray-200 bg-white p-1 shadow-lg dark:border-gray-700 dark:bg-gray-800">
      {choice(settings.chats !== 'all', 'Chats I started', () => setChats('mine'))}
      {choice(settings.chats === 'all', 'Every agent I can see', () => setChats('all'))}
      {on && !window.matchMedia?.('(pointer: coarse)').matches && <>
        <div className="my-1 border-t border-gray-200 dark:border-gray-700" />
        {choice(!!device?.sound, 'Play a sound', () => setSound(!device?.sound))}
      </>}
    </div>}
  </div>
}
