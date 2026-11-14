import { lazy, Suspense } from 'react'
const ConnectionsToolsPage = lazy(() => import('./ConnectionsToolsPage'))
import UsersPage from './UsersPage'
import McpConnectPage from './McpConnectPage'
import NotificationsPage from './NotificationsPage'
import { useAppStore } from '../stores/useAppStore'


/** The admin full page chosen in the top bar, or nothing. */
export default function AdminPages() {
  const page = useAppStore(state => state.adminPage)
  if (page === 'connections') return <Suspense fallback={<p className="p-6 text-sm text-muted-foreground">Loading connections…</p>}><ConnectionsToolsPage /></Suspense>
  if (page === 'users') return <UsersPage />
  if (page === 'mcp') return <McpConnectPage />
  if (page === 'notifications') return <NotificationsPage />
  return null
}
