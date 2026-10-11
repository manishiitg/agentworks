import UsersPage from './UsersPage'
import McpConnectPage from './McpConnectPage'
import { lazy, Suspense, useEffect } from 'react'
import { useAppStore } from '../stores/useAppStore'
import { isLocalProductInstallation } from '../products/productSurfaceConfig'

const MyVaultsPage = import.meta.env.VITE_DEPLOYMENT_MODE === 'local' ? () => null : lazy(() => import('./MyVaultsPage'))

/** The admin full page chosen in the top bar, or nothing. */
export default function AdminPages() {
  const page = useAppStore(state => state.adminPage)
  const local = isLocalProductInstallation()
  useEffect(() => {
    if (local && page === 'vaults') useAppStore.getState().setAdminPage(null)
  }, [local, page])
  if (page === 'users') return <UsersPage />
  if (page === 'mcp') return <McpConnectPage />
  if (page === 'vaults' && !local) return <Suspense fallback={null}><MyVaultsPage /></Suspense>
  return null
}
