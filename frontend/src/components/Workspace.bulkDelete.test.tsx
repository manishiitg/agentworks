// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import Workspace from './Workspace'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useModeStore } from '../stores/useModeStore'
import { useCapabilitiesStore } from '../stores/useCapabilitiesStore'
import { useAuthStore } from '../stores/useAuthStore'
import { agentApi } from '../services/api'

vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) }
  vi.stubGlobal('localStorage', storage)
  Object.defineProperty(window, 'localStorage', { configurable: true, value: storage })
})
vi.mock('../services/api', () => ({
  getApiBaseUrl: () => '', getAuthToken: () => null, getSessionId: () => '', resetSessionId: () => undefined,
  workspaceApi: {}, agentApi: {
    deletePlannerFile: vi.fn().mockResolvedValue({}),
    deletePlannerFolder: vi.fn().mockResolvedValue({}),
  },
}))
vi.mock('./ui/ConfirmationDialog', () => ({ default: ({ isOpen, onConfirm }: { isOpen: boolean; onConfirm: () => void }) => (
  isOpen ? <button onClick={onConfirm}>Confirm deletion</button> : null
) }))

it.each(['Chats/Code/projects/test-code', '/Chats/Code/projects/test-code///', '_users/alice/Chats/Code/projects/test-code'])('bulk deletes Code contents without deleting the project root or its metadata for %s', async scopedPath => {
  vi.clearAllMocks()
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const original = [useWorkspaceStore.getState(), useModeStore.getState(), useCapabilitiesStore.getState(), useAuthStore.getState()] as const
  const path = 'Chats/Code/projects/test-code'
  const fetchFiles = vi.fn().mockResolvedValue(undefined)
  useModeStore.setState({ selectedModeCategory: 'multi-agent' })
  useAuthStore.setState({ isMultiUserMode: true, user: { id: 'alice', is_admin: false } as NonNullable<ReturnType<typeof useAuthStore.getState>['user']> })
  useCapabilitiesStore.setState({ fetchCapabilities: vi.fn().mockResolvedValue(undefined) })
  useWorkspaceStore.setState({
    loading: false, error: null, searchQuery: '', needsRefresh: false, highlightedFile: null, fetchFiles,
    files: [{ filepath: `${path}/code`, type: 'folder', children: [{ filepath: `${path}/code/index.ts`, type: 'file' }] }, { filepath: path, type: 'folder', children: [
      { filepath: `${path}/product.json`, type: 'file' },
      { filepath: `${path}/workflow.json`, type: 'file' },
      { filepath: `${path}/app.ts`, type: 'file' },
      { filepath: `${path}/code`, type: 'folder', children: [{ filepath: `${path}/code/index.ts`, type: 'file' }] },
    ] }],
    expandedFolders: new Set(['test-code']),
  })
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<Workspace scopedWorkspacePath={scopedPath} hideRootActions hiddenRootFolders={['product.json', 'workflow.json']} hideAddToChat />))
    expect(Array.from(host.querySelectorAll('[data-filepath]')).map(row => row.getAttribute('data-filepath'))).toEqual(['test-code', 'code', 'app.ts'])
    const click = async (selector: string) => {
      const target = host.querySelector<HTMLElement>(selector)
      expect(target).not.toBeNull()
      await act(async () => target!.click())
    }
    await click('[aria-label="Select files"]')
    await click('[aria-label="Select all files"]')
    expect(host.querySelector('[aria-label="Delete selected files"]')?.textContent).toBe('2')
    await click('[aria-label="Delete selected files"]')
    await act(async () => (Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Confirm deletion')!).click())
    expect(agentApi.deletePlannerFile).toHaveBeenCalledExactlyOnceWith(`${path}/app.ts`)
    expect(agentApi.deletePlannerFolder).toHaveBeenCalledExactlyOnceWith(`${path}/code`)
    expect(fetchFiles).toHaveBeenLastCalledWith(scopedPath, { force: true })
    expect(host.querySelector('[aria-label="Delete selected files"]')).toBeNull()
  } finally {
    await act(async () => root.unmount()); host.remove()
    useWorkspaceStore.setState(original[0], true)
    useModeStore.setState(original[1], true)
    useCapabilitiesStore.setState(original[2], true)
    useAuthStore.setState(original[3], true)
    vi.unstubAllGlobals()
  }
})
