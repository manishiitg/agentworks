import { expect, it } from 'vitest'
import type { ActiveSessionInfo, RunFolderInfo, SessionExecutionTreeResponse } from '../../services/api-types'
import type { CrewExecutable } from './CrewExecutableEditor'
import { workflowRunTarget } from './workflowRunNavigation'

// PLAT-746: the monitor used to open the Crew chat, losing the clicked workflow run.
const session: ActiveSessionInfo = { session_id: 'session-run', crew_project_id: 'crew', workspace_path: 'Crew/example/automation/report', observer_id: '', agent_mode: 'workflow', status: 'running', last_activity: '', created_at: '' }
const tree: SessionExecutionTreeResponse = {
  session_id: session.session_id,
  root: { session_id: session.session_id, execution_id: 'session-root', source: 'session', kind: 'session', name: 'Report', status: 'running', started_at: '', children: [
    { session_id: session.session_id, execution_id: 'execution-chosen', source: 'workflow_run', kind: 'workflow_run', name: 'Same display name', status: 'running', started_at: '' },
    { session_id: 'another-session', execution_id: 'execution-other', source: 'workflow_run', kind: 'workflow_run', name: 'Same display name', status: 'running', started_at: '' },
  ] },
  summary: { session_id: session.session_id, session_status: 'running', display_status: 'busy', is_session_busy: true, running_count: 1, completed_count: 0, failed_count: 0, canceled_count: 0, has_running_main_agent: false, has_running_background_agents: false, has_running_tracked_executions: true },
}
const executables: CrewExecutable[] = [{ id: 'report', kind: 'script', workspace_path: session.workspace_path!, manifest: { label: 'Report' } }]
const folders: RunFolderInfo[] = [
  { name: 'run-newer-other', metadata: { execution_id: 'execution-other', created_at: '', status: 'running' } },
  { name: 'run-chosen', metadata: { execution_id: 'execution-chosen', created_at: '', status: 'running' } },
]

it('opens the saved run matching the same-session execution rather than the newest run or a same-named chat', () => {
  expect(workflowRunTarget(session, tree, folders, executables)).toEqual({ crewId: 'crew', executableId: 'report', workspacePath: session.workspace_path, runFolder: 'run-chosen' })
})

it('refuses another workspace, another session, missing records and ambiguous matches', () => {
  expect(workflowRunTarget({ ...session, workspace_path: 'Crew/other/automation/report' }, tree, folders, executables)).toBeNull()
  expect(workflowRunTarget(session, { ...tree, session_id: 'other' }, folders, executables)).toBeNull()
  expect(workflowRunTarget(session, tree, folders.slice(0, 1), executables)).toBeNull()
  expect(workflowRunTarget(session, tree, [...folders, { ...folders[1], name: 'run-duplicate' }], executables)).toBeNull()
})
