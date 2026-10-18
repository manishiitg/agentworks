import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { Inbox } from 'lucide-react'
import { EmployeeDashboard } from './EmployeeDashboard'

/** Activity page showing updates. Schedules live on their own page now. */
export default function ActivityPage() {
  return (
    <section aria-label="Goal activity" className="flex h-full min-h-0 flex-col bg-background">
      <WorkspacePageHeader page="activity" title="Goal activity" icon={Inbox} data-tour="activity-feed" description="Updates, decisions and recent runs across your Goals." />
      <div className="min-h-0 flex-1">
        <EmployeeDashboard />
      </div>
    </section>
  )
}
