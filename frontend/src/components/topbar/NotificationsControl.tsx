import { useEffect, useState } from 'react'
import { Bell, BellOff } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { playNotificationSound } from '../../utils/sound'
import { useIsElectron } from './useIsElectron'

/**
 * NotificationsControl - toggles desktop notifications + sound and surfaces the
 * OS permission state. Electron only; renders nothing in the browser.
 */
export default function NotificationsControl({ menuItem = false }: { menuItem?: boolean }) {
  const isElectron = useIsElectron()
  const [osPermission, setOsPermission] = useState<NotificationPermission>('default')
  const [notificationsEnabled, setNotificationsEnabled] = useState(() => {
    return window.localStorage.getItem('mcp_notifications_enabled') !== 'false' // Default true
  })

  useEffect(() => {
    if (!('Notification' in window)) return

    // Initial permission check + re-check when the window regains focus
    // (the user might have changed it in system settings).
    setOsPermission(Notification.permission)
    const handleFocus = () => setOsPermission(Notification.permission)
    window.addEventListener('focus', handleFocus)
    return () => window.removeEventListener('focus', handleFocus)
  }, [])

  if (!isElectron) return null

  const testNotification = () => {
    playNotificationSound()

    // Set Dock badge for test
    if (window.electronAPI?.setDockBadge) {
      window.electronAPI.setDockBadge('1')
      // Clear after 5 seconds for test
      setTimeout(() => {
        window.electronAPI?.setDockBadge?.('')
      }, 5000)
    }

    if (!('Notification' in window)) return

    if (Notification.permission === 'granted') {
      new Notification('Test Notification', {
        body: 'This is a test notification from AgentWorks',
        icon: '/logo.svg'
      })
    } else if (Notification.permission === 'default') {
      Notification.requestPermission().then(permission => {
        setOsPermission(permission)
        if (permission === 'granted') {
          new Notification('Test Notification', {
            body: 'This is a test notification from AgentWorks',
            icon: '/logo.svg'
          })
        }
      })
    }
  }

  const handleNotificationClick = () => {
    if (osPermission === 'denied') {
      alert('Notifications are blocked by your system settings. Please enable them in System Settings > Notifications > AgentWorks.')
      return
    }

    const nextValue = !notificationsEnabled
    setNotificationsEnabled(nextValue)
    window.localStorage.setItem('mcp_notifications_enabled', String(nextValue))

    if (nextValue) {
      // Just enabled: trigger test
      testNotification()
    }
  }

  if (menuItem) {
    return (
      <button
        type="button"
        role="menuitemcheckbox"
        aria-checked={notificationsEnabled && osPermission !== 'denied'}
        onClick={handleNotificationClick}
        className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-gray-700 transition-colors hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700"
      >
        {osPermission === 'denied' || !notificationsEnabled
          ? <BellOff className="h-4 w-4 text-muted-foreground" />
          : <Bell className="h-4 w-4 text-muted-foreground" />}
        <span className="flex-1">Notifications &amp; sound</span>
        <span className="text-xs text-muted-foreground">{osPermission === 'denied' ? 'Blocked' : notificationsEnabled ? 'On' : 'Off'}</span>
      </button>
    )
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={handleNotificationClick}
          aria-label="Notifications"
          className={`p-1.5 rounded-md transition-colors hover:bg-gray-100 dark:hover:bg-gray-700 ${
            osPermission === 'denied'
              ? 'text-red-500 hover:text-red-600'
              : notificationsEnabled
                ? 'text-indigo-500 hover:text-indigo-600 dark:text-indigo-400 dark:hover:text-indigo-300'
                : 'text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300'
          }`}
        >
          {osPermission === 'denied' || !notificationsEnabled ? <BellOff className="w-4 h-4" /> : <Bell className="w-4 h-4" />}
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">
        {osPermission === 'denied'
          ? 'Notifications blocked by system. Click to learn more.'
          : notificationsEnabled
            ? 'Disable Notifications'
            : 'Enable Notifications & Sound'}
      </TooltipContent>
    </Tooltip>
  )
}
