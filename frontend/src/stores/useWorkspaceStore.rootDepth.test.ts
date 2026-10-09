import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getPlannerFiles } = vi.hoisted(() => ({ getPlannerFiles: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: { getPlannerFiles: (...args: unknown[]) => getPlannerFiles(...args) } }))

import { useWorkspaceStore } from './useWorkspaceStore'

describe('workspace file tree scope', () => {
  beforeEach(() => {
    getPlannerFiles.mockReset()
    getPlannerFiles.mockResolvedValue({ success: true, message: '', data: [] })
    useWorkspaceStore.getState().setActiveFolder(null)
  })

  // A request with no folder used to walk the whole shared workspace
  // (server A: 652k files, 11-36 s on page open).
  it('caps a root request at two levels', async () => {
    await useWorkspaceStore.getState().fetchFiles(undefined, { force: true })
    expect(getPlannerFiles).toHaveBeenCalledWith(undefined, -1, 2)
  })

  it('keeps a folder request unlimited', async () => {
    await useWorkspaceStore.getState().fetchFiles('Workflow/reports', { force: true })
    expect(getPlannerFiles).toHaveBeenCalledWith('Workflow/reports', -1, undefined)
  })

  it('respects an explicit root depth', async () => {
    await useWorkspaceStore.getState().fetchFiles(undefined, { force: true, maxDepth: 1 })
    expect(getPlannerFiles).toHaveBeenCalledWith(undefined, -1, 1)
  })
})
