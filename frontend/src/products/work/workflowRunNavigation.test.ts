import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo, RunningWorkflowInfo, RunFolderInfo } from '../../services/api-types'
import type { CrewExecutable } from './CrewExecutableEditor'
import { workflowRunTarget } from './workflowRunNavigation'

describe('PLAT-746 direct workflow activity navigation', () => {
  it('opens the tracker-named run without a chat tree, never another run or workspace', () => {
    const session = { session_id: 'scheduled-run', crew_project_id: 'crew', workspace_path: 'Workflow/example' } as ActiveSessionInfo
    const run = { session_id: session.session_id, workspace_path: session.workspace_path, query_id: 'execution', run_folder: 'run-exact' } as RunningWorkflowInfo
    const executables = [{ id: 'program', workspace_path: session.workspace_path }] as CrewExecutable[]
    const folders = [{ name: 'run-other' }, { name: 'run-exact' }] as RunFolderInfo[]
    expect(workflowRunTarget(session, run, folders, executables)?.runFolder).toBe('run-exact')
    expect(workflowRunTarget(session, { ...run, session_id: 'another' }, folders, executables)).toBeNull()
    expect(workflowRunTarget(session, { ...run, workspace_path: 'Workflow/another' }, folders, executables)).toBeNull()
    expect(workflowRunTarget(session, run, folders.slice(0, 1), executables)).toBeNull()
    expect(workflowRunTarget(session, run, [...folders, folders[1]], executables)).toBeNull()
    expect(workflowRunTarget(session, run, [{ name: 'run-exact', metadata: { execution_id: 'another-execution' } } as RunFolderInfo], executables)).toBeNull()
  })
})
