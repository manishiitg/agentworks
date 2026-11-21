import { useEffect } from 'react'
import { useChatStore } from '../stores'
import { reportNotifyFocus } from '../services/notifications'
import { playNotificationSound } from '../utils/sound'

/**
 * Reports the native notification-active state on desktop (visible on Mac), or the focused
 * visible chat on the website. The server owns notification suppression.
 */
export function useNotifyFocus() {
  const session = useChatStore(state => (state.activeTabId ? state.chatTabs[state.activeTabId]?.sessionId : null) || null)
  useEffect(() => {
    const nativeWindow = window.electronAPI
    const report = () => {
      const focused = nativeWindow?.isNotificationWindowActive ? nativeWindow.isNotificationWindowActive() : document.hasFocus()
      reportNotifyFocus((nativeWindow?.isNotificationWindowActive ? focused : document.visibilityState === 'visible' && focused) ? session : null)
    }
    // Only website blur clears the report; a Mac window remains visible across monitors.
    const blur = () => nativeWindow?.isNotificationWindowActive ? report() : reportNotifyFocus(null)
    const stopNativeFocus = nativeWindow?.onNotificationWindowActiveChanged?.(focused => reportNotifyFocus(focused ? session : null))
    report()
    window.addEventListener('focus', report)
    window.addEventListener('blur', blur)
    document.addEventListener('visibilitychange', report)
    const timer = window.setInterval(report, 30_000)
    return () => {
      window.removeEventListener('focus', report)
      window.removeEventListener('blur', blur)
      stopNativeFocus?.()
      document.removeEventListener('visibilitychange', report)
      window.clearInterval(timer)
    }
  }, [session])
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return
    const onMessage = (event: MessageEvent) => {
      const active = window.electronAPI?.isNotificationWindowActive?.() ?? document.hasFocus()
      if (event.data?.type === 'agentworks-notify-sound' && !active) playNotificationSound()
    }
    navigator.serviceWorker.addEventListener('message', onMessage)
    return () => navigator.serviceWorker.removeEventListener('message', onMessage)
  }, [])
}
