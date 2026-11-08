// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { workflowManifestApi } from '../../services/api'
import type { GetWorkflowManifestResponse } from '../../services/api-types'
import WorkflowMissingSecretsNotice, { useWorkflowMissingSecrets } from './WorkflowMissingSecretsNotice'

vi.mock('../../services/api', () => ({ workflowManifestApi: { getWorkflowManifest: vi.fn() } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.resetAllMocks() })

function Notice({ workspacePath }: { workspacePath: string }) {
  return <WorkflowMissingSecretsNotice names={useWorkflowMissingSecrets(workspacePath)} />
}
function response(workspacePath: string, names: string[]): GetWorkflowManifestResponse {
  return { success: true, workspace_path: workspacePath, manifest: {} as GetWorkflowManifestResponse['manifest'], missing_secrets: names }
}

// PLAT-746-500: removing contract-upgrade status must keep this
// notice; an old workflow's names must also not appear in a new selection.
it('renders names from the manifest read and ignores a previous workflow response', async () => {
  let resolveOld!: (value: GetWorkflowManifestResponse) => void
  const oldResponse = new Promise<GetWorkflowManifestResponse>(resolve => { resolveOld = resolve })
  vi.mocked(workflowManifestApi.getWorkflowManifest).mockImplementation(async path => {
    if (path === 'Workflow/slow') return oldResponse
    return response(path, path === 'Workflow/empty' ? [] : ['CURRENT_KEY'])
  })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<Notice workspacePath="Workflow/current" />))
  expect(host.querySelector('[role="status"]')?.textContent).toContain('CURRENT_KEY')
  await act(async () => root.render(<Notice workspacePath="Workflow/slow" />))
  expect(host.querySelector('[role="status"]')).toBeNull()
  await act(async () => root.render(<Notice workspacePath="Workflow/next" />))
  expect(host.querySelector('[role="status"]')?.textContent).toContain('CURRENT_KEY')
  await act(async () => resolveOld(response('Workflow/slow', ['STALE_KEY'])))
  expect(host.textContent).not.toContain('STALE_KEY')
  expect(host.querySelector('[role="status"]')?.textContent).toContain('CURRENT_KEY')
  await act(async () => root.render(<Notice workspacePath="Workflow/empty" />))
  expect(host.querySelector('[role="status"]')).toBeNull()
})
