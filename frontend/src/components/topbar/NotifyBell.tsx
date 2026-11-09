import { useEffect, useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Bell, BellOff, Check } from 'lucide-react'
import { iconButtonClass } from '../ui/IconPopover'
import { useChatStore } from '../../stores'
import { playNotificationSound } from '../../utils/sound'
import {
  loadNotifySettings, notifyDeviceId, notifyErrorToast, turnNotificationsOff, turnNotificationsOn,
  type NotifySettings,
} from '../../services/notifications'

const isMacApp = () => !!window.electronAPI?.getDesktopConnection

/** One click-open menu; no overlapping tooltip, hover menu or scope picker. */
export default function NotifyBell() {
  const [settings, setSettings] = useState<NotifySettings | null>(null)
  const [busy, setBusy] = useState(false)
  const addToast = useChatStore(state => state.addToast)

  useEffect(() => {
    let cancelled = false
    loadNotifySettings().then(value => { if (!cancelled) setSettings(value) }).catch(() => {})
    return () => { cancelled = true }
  }, [])

  const device = settings?.devices.find(item => item.id === notifyDeviceId())
  const on = !!device
  const run = async (change: () => Promise<void>) => {
    if (busy) return
    setBusy(true)
    try { await change() } catch (error) { addToast(...notifyErrorToast(error)) }
    finally { setBusy(false) }
  }
  const toggle = () => run(async () => {
    if (!settings) return
    const devices = on ? await turnNotificationsOff() : await turnNotificationsOn(settings, isMacApp())
    setSettings({ ...settings, devices })
  })
  const setSound = () => run(async () => {
    if (!settings || !on) return
    const sound = !device?.sound
    setSettings({ ...settings, devices: await turnNotificationsOn(settings, sound) })
    if (sound) playNotificationSound()
  })
  const choice = (checked: boolean, label: string, action: () => void, disabled = false) => (
    <Menu.CheckboxItem checked={checked} disabled={disabled || busy} onCheckedChange={action} onSelect={event => event.preventDefault()}
      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50">
      <span className="h-3.5 w-3.5"><Menu.ItemIndicator><Check className="h-3.5 w-3.5" /></Menu.ItemIndicator></span>{label}
    </Menu.CheckboxItem>
  )
  return <Menu.Root>
    <Menu.Trigger asChild>
      <button type="button" aria-label="Notifications" disabled={busy || !settings} data-testid="notify-bell"
        className={`${iconButtonClass} ${on ? 'text-blue-600 dark:text-blue-400' : 'opacity-70'}`}>
        {on ? <Bell className="h-4 w-4" fill="currentColor" /> : <BellOff className="h-4 w-4" />}
      </button>
    </Menu.Trigger>
    <Menu.Portal><Menu.Content align="end" sideOffset={4} className="z-50 w-48 rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg">
      {choice(on, 'Show notifications', () => { void toggle() })}
      {choice(!!device?.sound, 'Play a sound', () => { void setSound() }, !on)}
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}
