import UsersPage from './UsersPage'
import McpConnectPage from './McpConnectPage'
import { useAppStore } from '../stores/useAppStore'

/** The admin full page chosen in the top bar, or nothing. */
export default function AdminPages() {
  const page = useAppStore(state => state.adminPage)
  if (page === 'users') return <UsersPage />
  if (page === 'mcp') return <McpConnectPage />
  return null
}
