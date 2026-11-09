import { useEffect } from 'react'
import { useChatStore } from '../stores'
import { reportNotifyFocus } from '../services/notifications'
import { playNotificationSound } from '../utils/sound'

/**
 * Tells the server which chat is in front on this device (none when the window is in the background), so that chat
 * gets no notification here, and plays the short sound a notification asks for while this tab is open behind others.
 */
export function useNotifyFocus() {
  const session = useChatStore(state => (state.activeTabId ? state.chatTabs[state.activeTabId]?.sessionId : null) || null)
  useEffect(() => {
    const nativeWindow = window.electronAPI
    const report = () => {
      const focused = nativeWindow?.isWindowFocused ? nativeWindow.isWindowFocused() : document.hasFocus()
      reportNotifyFocus(document.visibilityState === 'visible' && focused ? session : null)
    }
    // Clear explicitly on blur; document.hasFocus() can lag the blur event.
    const blur = () => reportNotifyFocus(null)
    const stopNativeFocus = nativeWindow?.onWindowFocusChanged?.(focused => reportNotifyFocus(focused && document.visibilityState === 'visible' ? session : null))
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
      if (event.data?.type === 'agentworks-notify-sound' && !document.hasFocus()) playNotificationSound()
    }
    navigator.serviceWorker.addEventListener('message', onMessage)
    return () => navigator.serviceWorker.removeEventListener('message', onMessage)
  }, [])
}
