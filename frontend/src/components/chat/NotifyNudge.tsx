import { useEffect, useState } from 'react'
import { useChatStore } from '../../stores'
import {
  NOTIFY_HOME_SCREEN_HINT, loadNotifySettings, notifyDeviceId, notifyErrorToast, notifyNeedsHomeScreen, turnNotificationsOn,
  type NotifySettings,
} from '../../services/notifications'

const SEEN_KEY = 'agentworks_notify_nudge_seen'

const seen = () => { try { return localStorage.getItem(SEEN_KEY) === '1' } catch { return true } }
const markSeen = () => { try { localStorage.setItem(SEEN_KEY, '1') } catch { /* shown for this page only */ } }

/**
 * The one-time phone nudge (owner, 5 Nov, approved mockup): after the person sends a message on a phone whose
 * notifications are off, offer the bell's own "Turn on" once per device. "Not now" hides it for good on this device;
 * Settings › Notifications stays available.
 */
export function NotifyNudge({ agentName }: { agentName: string }) {
  const [settings, setSettings] = useState<NotifySettings | null>(null)
  const [state, setState] = useState<'offer' | 'hint' | 'busy' | 'done'>('offer')
  const addToast = useChatStore(store => store.addToast)

  useEffect(() => {
    if (seen() || !window.matchMedia?.('(pointer: coarse)').matches || window.electronAPI?.getDesktopConnection) return
    let cancelled = false
    loadNotifySettings().then(value => {
      if (cancelled || seen()) return
      markSeen()
      if (!value.devices.some(device => device.id === notifyDeviceId())) setSettings(value)
    }).catch(() => { /* asked again on a later chat */ })
    return () => { cancelled = true }
  }, [])

  if (!settings || state === 'done') return null

  const turnOn = async () => {
    if (notifyNeedsHomeScreen()) { setState('hint'); return }
    setState('busy')
    try { await turnNotificationsOn(settings, false); setState('done') } catch (error) { addToast(...notifyErrorToast(error)); setState('offer') }
  }

  const button = 'rounded-full border border-border px-3 py-1 text-xs font-medium'
  return <div role="status" data-testid="notify-nudge" className="mx-3 mb-2 rounded-xl border border-border bg-card p-3 text-sm">
    {state === 'hint'
      ? <p className="text-xs text-muted-foreground">{NOTIFY_HOME_SCREEN_HINT}</p>
      : <>
        <p className="font-medium text-foreground">Get a notification when {agentName} finishes?</p>
        <p className="mt-0.5 text-xs text-muted-foreground">So you can close the app and come back when it's done.</p>
      </>}
    <div className="mt-2 flex gap-2">
      {state !== 'hint' && <button type="button" disabled={state === 'busy'} onClick={() => void turnOn()}
        className={`${button} border-primary bg-primary text-primary-foreground disabled:opacity-60`}>Turn on</button>}
      <button type="button" onClick={() => setState('done')} className={`${button} text-foreground hover:bg-muted`}>{state === 'hint' ? 'OK' : 'Not now'}</button>
    </div>
  </div>
}
