import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getPlannerFiles } = vi.hoisted(() => ({ getPlannerFiles: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: { getPlannerFiles: (...args: unknown[]) => getPlannerFiles(...args) } }))

import { useWorkspaceStore } from './useWorkspaceStore'
import { fileSearchTree } from '../utils/lazyWorkspaceTree'

describe('workspace file tree scope', () => {
  beforeEach(() => {
    getPlannerFiles.mockReset()
    getPlannerFiles.mockResolvedValue({ success: true, message: '', data: [] })
    useWorkspaceStore.getState().resetWorkspaceState()
  })

  // A request with no folder used to walk the whole shared workspace
  // (server A: 652k files, 11-36 s on page open).
  it('loads direct children only at the workspace root', async () => {
    await useWorkspaceStore.getState().fetchFiles(undefined, { force: true })
    expect(getPlannerFiles).toHaveBeenCalledWith(undefined, -1, 0, {})
  })

  it('loads direct children only for project folders', async () => {
    await useWorkspaceStore.getState().fetchFiles('Workflow/reports', { force: true })
    expect(getPlannerFiles).toHaveBeenCalledWith('Workflow/reports', -1, 0, {})
  })

  it('respects an explicit root depth', async () => {
    await useWorkspaceStore.getState().fetchFiles(undefined, { force: true, maxDepth: 1 })
    expect(getPlannerFiles).toHaveBeenCalledWith(undefined, -1, 1, {})
  })
})


it('loads a nested branch, refreshes only expanded folders and retries failed loads', async () => {
  const root = 'Crew/example/_files'
  let fail = false
  let text = 'old.txt'
  getPlannerFiles.mockImplementation(async (path: string) => {
    if (fail && path.endsWith('/src')) throw new Error('Cannot read folder')
    return { success: true, data: path === root
      ? [{ filepath: root, type: 'folder', children: [
          { filepath: `${root}/src`, type: 'folder', has_children: true },
          { filepath: `${root}/closed`, type: 'folder', has_children: true },
          { filepath: `${root}/empty`, type: 'folder', has_children: false },
        ] }]
      : [{ filepath: path, type: 'folder', children: [{ filepath: `${path}/${text}`, type: 'file' }] }] }
  })
  const store = useWorkspaceStore.getState()
  store.resetWorkspaceState(); store.setActiveFolder(root)
  await store.fetchFiles(root, { force: true })
  expect(useWorkspaceStore.getState().fileIndex.get(`${root}/src`)?.children).toBeUndefined()
  expect(useWorkspaceStore.getState().fileIndex.get(`${root}/empty`)?.children).toEqual([])
  fail = true
  await store.loadFolder(`${root}/src`)
  expect(useWorkspaceStore.getState().folderLoads[`${root}/src`].error).toContain('Cannot read folder')
  expect(useWorkspaceStore.getState().fileIndex.get(`${root}/src`)?.has_children).toBe(true)
  fail = false
  await store.loadFolder(`${root}/src`, true)
  expect(useWorkspaceStore.getState().fileIndex.has(`${root}/src/old.txt`)).toBe(true)
  expect(useWorkspaceStore.getState().fileIndex.has(`${root}/closed`)).toBe(true)
  store.setExpandedFolders(new Set(['_files', 'src']))
  getPlannerFiles.mockClear(); text = 'new.txt'
  await store.refreshFiles(root)
  expect(getPlannerFiles.mock.calls.map(call => call[0])).toEqual([root, `${root}/src`])
  expect(useWorkspaceStore.getState().fileIndex.has(`${root}/src/new.txt`)).toBe(true)
  expect(useWorkspaceStore.getState().fileIndex.has(`${root}/src/old.txt`)).toBe(false)
  expect(useWorkspaceStore.getState().fileIndex.get(`${root}/closed`)?.children).toBeUndefined()
})


it('keeps neighboring roots when opening a folder in the unscoped workspace', async () => {
  const store = useWorkspaceStore.getState()
  store.resetWorkspaceState()
  getPlannerFiles.mockImplementation(async (path?: string) => ({ success: true, data: path
    ? [{ filepath: path, type: 'folder', children: [{ filepath: `${path}/notes.txt`, type: 'file' }] }]
    : [{ filepath: 'Chats', type: 'folder', has_children: true }, { filepath: 'Downloads', type: 'folder', has_children: true }] }))
  await store.fetchFiles(undefined, { force: true })
  await store.loadFolder('Chats')
  expect(useWorkspaceStore.getState().files.map(file => file.filepath)).toEqual(['Chats', 'Downloads'])
  expect(useWorkspaceStore.getState().fileIndex.has('Chats/notes.txt')).toBe(true)
  expect(fileSearchTree([{ filepath: 'README.md', type: 'file' }]).map(file => file.filepath)).toEqual(['README.md'])
})
