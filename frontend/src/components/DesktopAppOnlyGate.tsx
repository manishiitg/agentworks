import { LogOut, MonitorDown } from 'lucide-react'
import { DesktopConnectButton } from './DesktopConnectButton'
import { Button } from './ui/Button'
import { useAuthStore } from '../stores/useAuthStore'

export function DesktopAppOnlyGate() {
  const { user, logout } = useAuthStore()

  return (
    <div className="min-h-screen bg-gray-100 px-4 py-10 dark:bg-gray-950">
      <div className="mx-auto flex min-h-[calc(100vh-5rem)] max-w-3xl items-center justify-center">
        <div className="w-full rounded-lg border border-gray-200 bg-white p-6 shadow-xl dark:border-slate-800 dark:bg-slate-900">
          <div className="mb-5 flex items-start justify-between gap-4">
            <div className="flex items-center gap-3">
              <div className="flex h-11 w-11 items-center justify-center rounded-lg bg-blue-50 text-blue-600 dark:bg-blue-950/40 dark:text-blue-300">
                <MonitorDown className="h-5 w-5" />
              </div>
              <div>
                <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-100">Open this workspace in the Mac app</h1>
                {user && (
                  <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    Signed in as {user.email || user.username || 'current user'}
                  </p>
                )}
              </div>
            </div>
            <Button type="button" variant="outline" size="sm" className="shrink-0 gap-2" onClick={logout}>
              <LogOut className="h-4 w-4" />
              Sign out
            </Button>
          </div>

          <p className="mb-5 text-sm leading-6 text-gray-600 dark:text-gray-300">
            This server is configured for desktop-only use. Download and open the Mac app first, then choose Connect server in its sidebar.
          </p>

          <DesktopConnectButton />
        </div>
      </div>
    </div>
  )
}
