import { createContext, useContext } from 'react'
import { useStore } from 'zustand'
import { useWorkspaceGitStore, type WorkspaceGitState } from '../../stores/useWorkspaceGitStore'
import { workspaceGitApi, type GitAction } from '../../services/workspaceGit'
import { openWorkspaceFile } from '../../utils/openWorkspaceFile'
import { agentApi } from '../../services/api'

export interface FileGitSource {
  store: typeof useWorkspaceGitStore
  api: typeof workspaceGitApi
  openFile: (path: string) => void | Promise<void>
  readFile: (path: string) => Promise<string>
  remoteAction?: (op: 'pull' | 'push') => GitAction
  writable?: boolean
}
const defaultSource: FileGitSource = {
  store: useWorkspaceGitStore, api: workspaceGitApi, openFile: openWorkspaceFile,
  readFile: async path => String((await agentApi.getPlannerFileContent(path)).data?.content ?? ''),
}
export const FileGitContext = createContext<FileGitSource>(defaultSource)
export const useFileGit = () => useContext(FileGitContext)
export function useFileGitStore<T>(selector: (state: WorkspaceGitState) => T): T {
  return useStore(useFileGit().store, selector)
}
