// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
vi.mock('../../../services/api', () => ({ agentApi: {}, getApiBaseUrl: () => 'http://127.0.0.1:99999' }))
import { useWorkflowStore } from '../../../stores/useWorkflowStore'
import { useDefaultHumanActionsView } from './useDefaultHumanActionsView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

function Probe({ workspacePath, loaded, count }: { workspacePath: string; loaded: boolean; count: number }) {
  useDefaultHumanActionsView(workspacePath, loaded, count)
  return null
}

it('opens Human actions once when the first decision count has pending items', async () => {
  const original = useWorkflowStore.getState()
  const openWorkspaceView = vi.fn()
  useWorkflowStore.setState({ workflowWorkspaceView: 'report', openWorkspaceView })
  const root = createRoot(document.createElement('div'))
  try {
    await act(async () => root.render(<Probe workspacePath="Workflow/a" loaded={false} count={0} />))
    expect(openWorkspaceView).not.toHaveBeenCalled()
    await act(async () => root.render(<Probe workspacePath="Workflow/a" loaded count={2} />))
    expect(openWorkspaceView).toHaveBeenCalledExactlyOnceWith('human-actions')
    await act(async () => root.render(<Probe workspacePath="Workflow/a" loaded count={3} />))
    expect(openWorkspaceView).toHaveBeenCalledTimes(1)
  } finally {
    await act(async () => root.unmount())
    useWorkflowStore.setState({ workflowWorkspaceView: original.workflowWorkspaceView, openWorkspaceView: original.openWorkspaceView })
  }
})

it('keeps a view selected while the first count was loading', async () => {
  const original = useWorkflowStore.getState()
  const openWorkspaceView = vi.fn()
  useWorkflowStore.setState({ workflowWorkspaceView: 'report', openWorkspaceView })
  const root = createRoot(document.createElement('div'))
  try {
    await act(async () => root.render(<Probe workspacePath="Workflow/b" loaded={false} count={0} />))
    useWorkflowStore.setState({ workflowWorkspaceView: 'flow' })
    await act(async () => root.render(<Probe workspacePath="Workflow/b" loaded count={1} />))
    expect(openWorkspaceView).not.toHaveBeenCalled()
  } finally {
    await act(async () => root.unmount())
    useWorkflowStore.setState({ workflowWorkspaceView: original.workflowWorkspaceView, openWorkspaceView: original.openWorkspaceView })
  }
})
