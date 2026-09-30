import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../services/api', () => ({ agentApi: {} }))
import { useWorkspaceStore } from './useWorkspaceStore'

const tab = (path: string) => ({ name: path.split('/').pop()!, path })

beforeEach(() => useWorkspaceStore.getState().resetWorkspaceState())

describe('open tabs belong to one workspace', () => {
  it('drops tabs from another project and closes the viewer when its file is gone', () => {
    const store = useWorkspaceStore.getState()
    store.setSelectedFile(tab('Chats/Code/projects/a/src/a.ts'))
    store.setSelectedFile(tab('Chats/Code/projects/b/src/b.ts'))
    store.setShowFileContent(true)
    expect(useWorkspaceStore.getState().openTabs).toHaveLength(2)

    useWorkspaceStore.getState().pruneOpenTabs('Chats/Code/projects/a')
    const after = useWorkspaceStore.getState()
    expect(after.openTabs.map(item => item.path)).toEqual(['Chats/Code/projects/a/src/a.ts'])
    // The open file (b.ts) is outside project a: the viewer closes.
    expect(after.selectedFile).toBeNull()
    expect(after.showFileContent).toBe(false)
  })

  it('keeps everything when all tabs are inside the workspace, tolerating slashes', () => {
    const store = useWorkspaceStore.getState()
    store.setSelectedFile(tab('Chats/Code/projects/a/x.ts'))
    store.setShowFileContent(true)
    useWorkspaceStore.getState().pruneOpenTabs('/Chats/Code/projects/a/')
    const after = useWorkspaceStore.getState()
    expect(after.openTabs).toHaveLength(1)
    expect(after.selectedFile?.path).toBe('Chats/Code/projects/a/x.ts')
    expect(after.showFileContent).toBe(true)
  })

  it('does not treat a sibling with the same prefix as inside the workspace', () => {
    useWorkspaceStore.getState().setSelectedFile(tab('Chats/Code/projects/ab/x.ts'))
    useWorkspaceStore.getState().pruneOpenTabs('Chats/Code/projects/a')
    expect(useWorkspaceStore.getState().openTabs).toHaveLength(0)
  })
})
