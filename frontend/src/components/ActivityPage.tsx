import { LayoutDashboard } from 'lucide-react'
import { EmployeeDashboard } from './EmployeeDashboard'
import { WorkspaceBackButton } from './workspace/WorkspaceBackButton'

/** Activity page showing updates. Schedules live on their own page now. */
export default function ActivityPage() {
  return (
    <section aria-label="Activity" className="flex h-full min-h-0 flex-col bg-background">
      <header data-tour="activity-feed" className="flex shrink-0 flex-wrap items-center gap-x-6 border-b border-border px-4 sm:px-6">
        <div className="flex items-center gap-2 py-3">
          <WorkspaceBackButton />
          <span aria-hidden="true" className="mx-1 h-4 w-px bg-border" />
          <LayoutDashboard className="h-4 w-4 text-primary" />
          <div>
            <h1 className="text-base font-semibold text-foreground">Activity</h1>
            <p className="sr-only">Updates and decisions across your automations.</p>
          </div>
        </div>
      </header>
      <div className="min-h-0 flex-1">
        <EmployeeDashboard />
      </div>
    </section>
  )
}
