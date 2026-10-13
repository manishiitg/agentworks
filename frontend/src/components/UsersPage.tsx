import { WorkspaceBackButton } from './workspace/WorkspaceBackButton'
import { Users } from 'lucide-react'
import UsersAdminPanel from './admin/UsersAdminPanel'
import { useAuthStore } from '../stores/useAuthStore'

/**
 * Shared directory opened from every product's navigation rail. Administrators manage roles, products and
 * create permissions here; everyone else sees the directory.
 */
export default function UsersPage() {
  const isAdmin = useAuthStore((s) => Boolean(s.user?.is_admin))
  return (
    <section aria-label="Users and access" className="flex h-full min-h-0 flex-col bg-background">
      <header className="shrink-0 border-b border-border px-4 sm:px-6">
        <div className="flex flex-wrap items-center gap-3 py-3">
          <WorkspaceBackButton />
          <span aria-hidden="true" className="h-4 w-px bg-border" />
          <Users className="h-4 w-4 text-primary" />
          <h1 className="text-sm font-semibold text-foreground">Users &amp; access</h1>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-5xl p-4 sm:p-6">
          <UsersAdminPanel vaultOnly={!isAdmin} />
        </div>
      </div>
    </section>
  )
}
