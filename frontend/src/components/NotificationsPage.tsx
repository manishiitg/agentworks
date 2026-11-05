import { useEffect, useState } from 'react'
import { Bell, Laptop, Smartphone } from 'lucide-react'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { SettingsEmpty } from './ui/SettingsCard'
import { ToggleRow } from './ui/ToggleRow'
import { useChatStore } from '../stores'
import { playNotificationSound } from '../utils/sound'
import {
  loadNotifySettings, notifyDeviceId, notifyErrorToast, removeNotifyDevice, saveNotifyChats, saveNotifyNeedsMe,
  turnNotificationsOff, turnNotificationsOn, type NotifyChats, type NotifyDevice, type NotifySettings,
} from '../services/notifications'

const isMacApp = () => !!window.electronAPI?.getDesktopConnection
const isPhoneLabel = (device: NotifyDevice) => /iPhone|iPad|Android/.test(device.label)

/**
 * Settings › Notifications (owner, 5 Nov, approved mockup): the same switch as the bell for this device, which chats,
 * "needs me" alerts, the sound on a computer, and every device of this person. The server decides every send.
 */
export default function NotificationsPage() {
  const [settings, setSettings] = useState<NotifySettings | null>(null)
  const [failed, setFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  const addToast = useChatStore(state => state.addToast)

  const load = () => {
    setFailed(false)
    loadNotifySettings().then(setSettings).catch(() => setFailed(true))
  }
  useEffect(() => { load() }, [])

  const thisDevice = notifyDeviceId()
  const device = settings?.devices.find(item => item.id === thisDevice)
  const on = !!device
  const computer = !window.matchMedia?.('(pointer: coarse)').matches

  const run = async (change: (current: NotifySettings) => Promise<void>) => {
    if (busy || !settings) return
    setBusy(true)
    try { await change(settings) } catch (error) { addToast(...notifyErrorToast(error)) }
    finally { setBusy(false) }
  }

  const setOn = (value: boolean) => run(async current => {
    const devices = value ? await turnNotificationsOn(current, isMacApp()) : await turnNotificationsOff()
    setSettings({ ...current, devices })
  })
  const setChats = (chats: NotifyChats) => run(async () => setSettings(await saveNotifyChats(chats)))
  const setNeedsMe = (needsMe: boolean) => run(async () => setSettings(await saveNotifyNeedsMe(needsMe)))
  const setSound = (sound: boolean) => run(async current => {
    setSettings({ ...current, devices: await turnNotificationsOn(current, sound) })
    if (sound) playNotificationSound()
  })
  const remove = (id: string) => run(async current => setSettings({ ...current, devices: await removeNotifyDevice(id) }))

  return (
    <section aria-label="Notifications" className="flex h-full min-h-0 flex-col bg-background">
      <WorkspacePageHeader page="notifications" title="Notifications" icon={Bell} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-2xl p-4 sm:p-6">
          {!settings ? (
            failed
              ? <p role="alert" className="text-sm text-muted-foreground">Notification settings could not be loaded. <button type="button" onClick={load} className="text-primary hover:underline">Try again</button></p>
              : <p className="text-sm text-muted-foreground">Loading…</p>
          ) : <section aria-label="Notification settings" className="space-y-3 rounded-lg border border-border p-4 text-xs">
            <div className="divide-y divide-border">
              <div className="pb-3">
                <ToggleRow label="Notify me when an agent finishes" description="On this device" checked={on} disabled={busy} onCheckedChange={setOn} />
              </div>
              <div className="py-3">
                <p className="text-muted-foreground">Which chats</p>
                <span role="radiogroup" aria-label="Which chats" className="mt-1.5 inline-flex overflow-hidden rounded-full border border-border">
                  {([['mine', 'Chats I started'], ['all', 'Every agent I can see']] as const).map(([value, label]) => <button key={value} type="button" role="radio" disabled={busy}
                    aria-checked={(settings.chats === 'all' ? 'all' : 'mine') === value} onClick={() => setChats(value)}
                    className={`px-3 py-1 text-xs ${(settings.chats === 'all' ? 'all' : 'mine') === value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>)}
                </span>
              </div>
              <div className="py-3">
                <ToggleRow label="Also tell me when an agent needs me (a question or approval)" checked={settings.needs_me} disabled={busy} onCheckedChange={setNeedsMe} />
              </div>
              {on && computer && <div className="py-3">
                <ToggleRow label="Play a sound" description="On this device" checked={!!device?.sound} disabled={busy} onCheckedChange={setSound} />
              </div>}
              <div className="pt-3">
                <p className="mb-1.5 text-muted-foreground">Your devices</p>
                {settings.devices.length === 0 ? <SettingsEmpty>No devices yet. Turn on notifications above.</SettingsEmpty> : <ul className="space-y-1">
                  {settings.devices.map(item => <li key={item.id} className="flex items-center gap-2 py-1 text-sm">
                    {isPhoneLabel(item) ? <Smartphone className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /> : <Laptop className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
                    <span className="min-w-0 truncate">{item.label}</span>
                    {item.id === thisDevice && <span className="shrink-0 text-xs text-muted-foreground">· this device</span>}
                    <button type="button" disabled={busy} onClick={() => remove(item.id)} aria-label={`Remove ${item.label}`}
                      className="ml-auto shrink-0 rounded px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">Remove</button>
                  </li>)}
                </ul>}
              </div>
            </div>
            <p className="text-muted-foreground">Not sent for a chat you have open in front of you. The notification says which agent and Crew, never the chat's text.</p>
          </section>}
        </div>
      </div>
    </section>
  )
}
