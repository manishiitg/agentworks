import { useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { useWorkspacePanelChatDraft } from '../../components/workspace/WorkspacePanelNavigation'
import { useCrewPanel } from './useCrewPanel'
import { ProjectTaskPage, ProjectTasksPanel } from './ProjectTasksRow'

// Project tasks at full width (#4314), opened like the Team page from the
// side panel's open button: the lead's focus, Needs you and the lead's groups
// side by side on wide screens; a task opens its page here too. The data is the
// same project panel read the side panel uses, so both show the same list.
export function ProjectTasksPage({ projectId, workspacePath = '', onBack }: { projectId: string; workspacePath?: string; onBack?: () => void }) {
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = projectMenuHistoryScope(server || 'local', user?.id || 'pending', 'work', projectId || workspacePath)
  const { data, error } = useCrewPanel(scope, projectId, workspacePath)
  const draft = useWorkspacePanelChatDraft()
  const onAgent = (message: string) => { void draft(message).catch(() => undefined) }
  const [taskId, setTaskId] = useState<string>()
  const row = data?.sections.find(section => section.id === 'activity')?.rows.find(item => item.group === 'project-tasks')
  const task = taskId ? row?.tasks?.find(item => item.id === taskId) : undefined
  const back = task ? () => setTaskId(undefined) : onBack
  return <section aria-label="Project tasks" className="mx-auto max-w-5xl space-y-3 text-sm">
    <div className="flex items-center gap-2">
      {back && <button type="button" aria-label={task ? 'Back to Project tasks' : 'Back to workspace'} title="Back" onClick={back}
        className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowLeft aria-hidden="true" className="h-4 w-4"/></button>}
      <h2 className="min-w-0 flex-1 truncate text-base font-semibold text-foreground">{task ? task.title : 'Project tasks'}</h2>
    </div>
    {!row ? <p role="status" className="text-sm text-muted-foreground">{error || (data ? 'This project has no task list yet. Set up Project activity from the side panel.' : 'Loading…')}</p>
      : task ? <div className="max-w-2xl"><ProjectTaskPage row={row} task={task} onAgent={onAgent}/></div>
      : <ProjectTasksPanel wide row={row} storageKey={`agentworks.task-groups-folded:${projectId || workspacePath}`} onAgent={onAgent} onOpenTask={setTaskId}/>}
  </section>
}
