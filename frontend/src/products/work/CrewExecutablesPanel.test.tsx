// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { RunsView } from './CrewWorkflowRuns'
import { useWorkflowRunNavigation } from './workflowRunNavigation'

vi.mock('../../services/api', () => ({ default: { get: async () => ({ data: { executables: [{ id: 'report', kind: 'script', workspace_path: 'Crew/example/automation/report', manifest: { label: 'Report' } }] } }) } }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: { getState: () => ({ refreshWorkflows: async () => {} }) } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: async () => {} }))
// Keep the page and its real back control; replace the network-heavy editor at its controlled-view boundary.
vi.mock('./CrewExecutableEditor', () => ({ CrewExecutableEditor: ({ runs, onRuns }: { runs: RunsView; onRuns: (view: RunsView) => void }) => <div>
  <output>{runs.run ?? 'run list'} · {runs.detail ?? 'summary'}</output>
  <button onClick={() => onRuns({ ...runs, detail: 'log' })}>Full log</button>
  <button onClick={() => onRuns({ ...runs, detail: 'costs' })}>Cost details</button>
</div> }))
import { CrewExecutablesPanel } from './CrewExecutablesPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746: the page arrow used to close the run while Full log / Cost details was open.
it('returns from log and cost details to the same run before returning to the run list', async () => {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  useWorkflowRunNavigation.setState({ target: { crewId: 'crew', executableId: 'report', workspacePath: 'Crew/example/automation/report', runFolder: 'run-chosen' } })
  try {
    await act(async () => root.render(<CrewExecutablesPanel projectId="crew" chatTabId="chat" />))
    for (const label of ['Full log', 'Cost details']) {
      const button = [...host.querySelectorAll('button')].find(item => item.textContent === label)!
      await act(async () => button.click())
      expect(host.querySelector('output')?.textContent).toContain('run-chosen')
      await act(async () => host.querySelector<HTMLButtonElement>('[title="Back"]')!.click())
      expect(host.querySelector('output')?.textContent).toBe('run-chosen · summary')
    }
    await act(async () => host.querySelector<HTMLButtonElement>('[title="Back"]')!.click())
    expect(host.querySelector('output')?.textContent).toBe('run list · summary')
  } finally {
    await act(async () => root.unmount()); host.remove(); useWorkflowRunNavigation.setState({ target: null })
  }
})
