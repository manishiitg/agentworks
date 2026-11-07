import { getApiBaseUrl, getAuthToken, getDesktopApiBaseUrl } from './api'

// Notifications (owner, 5 Nov): the person turns them on per device; the server decides every send
// (agent_go/cmd/server/notifications.go). This file only registers this device and says which chat is in front.

export type NotifyChats = 'mine' | 'all'

export interface NotifyDevice {
  id: string
  kind: 'web' | 'mac'
  label: string
  sound: boolean
  added: string
  last_sent?: string
}

export interface NotifySettings {
  chats: NotifyChats
  needs_me: boolean
  devices: NotifyDevice[]
  vapid_public_key: string
}

const DEVICE_KEY = 'agentworks_notify_device'

const isMacApp = () => typeof window !== 'undefined' && !!window.electronAPI?.getDesktopConnection

// The Mac app's device lives on its own server, which relays its company servers; a browser uses the server it shows.
const deviceBase = () => (isMacApp() ? getDesktopApiBaseUrl() : getApiBaseUrl())

export function notifyDeviceId(): string {
  let id = ''
  try { id = localStorage.getItem(DEVICE_KEY) || '' } catch { /* private window */ }
  if (!id) {
    id = crypto.randomUUID()
    try { localStorage.setItem(DEVICE_KEY, id) } catch { /* kept for this page only */ }
  }
  return id
}

async function call<T>(base: string, path: string, method = 'GET', body?: unknown): Promise<T> {
  const token = getAuthToken()
  const response = await fetch(`${base}${path}`, {
    method,
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!response.ok) {
    let message = 'Notifications could not be changed. Try again.'
    try { message = (await response.json()).error || message } catch { /* plain text */ }
    throw new Error(message)
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

export const loadNotifySettings = () => call<NotifySettings>(deviceBase(), '/api/notifications')

export const saveNotifyChats = (chats: NotifyChats) => call<NotifySettings>(deviceBase(), '/api/notifications', 'PUT', { chats })

export const saveNotifyNeedsMe = (needsMe: boolean) => call<NotifySettings>(deviceBase(), '/api/notifications', 'PUT', { needs_me: needsMe })

// removeNotifyDevice stops notifications on one of the person's devices; this device also drops its subscription.
export async function removeNotifyDevice(id: string): Promise<NotifyDevice[]> {
  if (id === notifyDeviceId()) return turnNotificationsOff()
  return (await call<{ devices: NotifyDevice[] }>(deviceBase(), `/api/notifications/devices/${encodeURIComponent(id)}`, 'DELETE')).devices
}

function deviceLabel(): string {
  if (isMacApp()) return 'This Mac app'
  const ua = navigator.userAgent
  const system = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : 'Linux'
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  return `${system} · ${browser}`
}

function base64UrlToBytes(value: string): Uint8Array {
  const padded = (value + '='.repeat((4 - (value.length % 4)) % 4)).replace(/-/g, '+').replace(/_/g, '/')
  return Uint8Array.from(atob(padded), c => c.charCodeAt(0))
}

// An iPhone or iPad shows notifications only for a site opened from its Home Screen (Apple's rule).
export const NOTIFY_HOME_SCREEN_HINT = 'First add AgentWorks to your Home Screen (Share › Add to Home Screen), then open it from there and tap Turn on.'

// notifyErrorToast words a failed change for the toast; the Home Screen step is advice, not an error.
export function notifyErrorToast(error: unknown): [string, 'info' | 'error'] {
  const message = error instanceof Error && error.message ? error.message : 'Notifications could not be changed. Try again.'
  return [message, message === NOTIFY_HOME_SCREEN_HINT ? 'info' : 'error']
}

export function notifyNeedsHomeScreen(): boolean {
  if (typeof window === 'undefined' || isMacApp()) return false
  const apple = /iPhone|iPad|iPod/.test(navigator.userAgent) || (/Macintosh/.test(navigator.userAgent) && navigator.maxTouchPoints > 1)
  const standalone = window.matchMedia?.('(display-mode: standalone)').matches || (navigator as Navigator & { standalone?: boolean }).standalone === true
  return apple && !standalone
}

export function webPushSupported(): boolean {
  return typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

// turnNotificationsOn asks this browser for permission (the Mac app uses native OS notifications) and registers this device.
export async function turnNotificationsOn(settings: NotifySettings, sound: boolean): Promise<NotifyDevice[]> {
  const id = notifyDeviceId()
  if (isMacApp()) {
    return (await call<{ devices: NotifyDevice[] }>(deviceBase(), '/api/notifications/devices', 'POST', { id, kind: 'mac', label: deviceLabel(), sound })).devices
  }
  if (notifyNeedsHomeScreen()) throw new Error(NOTIFY_HOME_SCREEN_HINT)
  if (!webPushSupported()) throw new Error("This browser can't show notifications.")
  const permission = await Notification.requestPermission()
  if (permission !== 'granted') throw new Error('Notifications are blocked for this site. Allow them in the browser\'s site settings, then try again.')
  const registration = await navigator.serviceWorker.register('/notify-sw.js')
  await navigator.serviceWorker.ready
  let subscription = await registration.pushManager.getSubscription()
  if (!subscription) {
    subscription = await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: base64UrlToBytes(settings.vapid_public_key) as BufferSource })
  }
  const json = subscription.toJSON()
  return (await call<{ devices: NotifyDevice[] }>(deviceBase(), '/api/notifications/devices', 'POST', {
    id, kind: 'web', label: deviceLabel(), endpoint: json.endpoint, keys: json.keys, sound,
  })).devices
}

export async function turnNotificationsOff(): Promise<NotifyDevice[]> {
  const devices = (await call<{ devices: NotifyDevice[] }>(deviceBase(), `/api/notifications/devices/${encodeURIComponent(notifyDeviceId())}`, 'DELETE')).devices
  if (!isMacApp() && webPushSupported()) {
    try {
      const registration = await navigator.serviceWorker.getRegistration('/notify-sw.js')
      await (await registration?.pushManager.getSubscription())?.unsubscribe()
    } catch { /* the server no longer sends to it either way */ }
  }
  return devices
}

// reportNotifyFocus tells the server shown on screen which chat is in front on this device (or none), so that chat
// gets no notification here.
// The server forgets a focus after two minutes, so an unchanged one is repeated every minute.
let lastFocus = ''
let lastFocusAt = 0
export function reportNotifyFocus(session: string | null) {
  const key = `${getApiBaseUrl()}|${session || ''}`
  if (key === lastFocus && (!session || Date.now() - lastFocusAt < 60_000)) return
  lastFocus = key
  lastFocusAt = Date.now()
  void call(getApiBaseUrl(), '/api/notifications/focus', 'POST', { device: notifyDeviceId(), session: session || '' }).catch(() => { lastFocus = '' })
}
