import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { Users } from 'lucide-react'
import UsersAdminPanel from './admin/UsersAdminPanel'
import CrewStoragePanel from './admin/CrewStoragePanel'
import { useAuthStore } from '../stores/useAuthStore'

/**
 * Shared directory opened from every product's navigation rail. Administrators manage roles, products and
 * create permissions here; everyone else sees the directory.
 */
export default function UsersPage() {
  const isAdmin = useAuthStore((s) => Boolean(s.user?.is_admin))
  return (
    <section aria-label="Users and access" className="flex h-full min-h-0 flex-col bg-background">
      <WorkspacePageHeader page="users" title="People & access" icon={Users} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-5xl p-4 sm:p-6">
          <UsersAdminPanel vaultOnly={!isAdmin} />
          {isAdmin ? <CrewStoragePanel /> : null}
        </div>
      </div>
    </section>
  )
}
