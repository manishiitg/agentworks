import UsersPage from './UsersPage'
import McpConnectPage from './McpConnectPage'
import { lazy, Suspense } from 'react'
import { useAppStore } from '../stores/useAppStore'

const MyVaultsPage = lazy(() => import('./MyVaultsPage'))

/** The admin full page chosen in the top bar, or nothing. */
export default function AdminPages() {
  const page = useAppStore(state => state.adminPage)
  if (page === 'users') return <UsersPage />
  if (page === 'mcp') return <McpConnectPage />
  if (page === 'vaults') return <Suspense fallback={null}><MyVaultsPage /></Suspense>
  return null
}
