import { useEffect, useMemo, useRef, useState } from 'react'
import api from '../../services/api'
import { createWorkspaceGitStore } from '../../stores/useWorkspaceGitStore'
import type { FileGitSource } from '../../components/workspace/FileGitContext'
import type { ReadOnlyFileWorkspaceSource } from '../../components/workspace/fileWorkspaceSource'
import type { GitAction, GitRepo } from '../../services/workspaceGit'

/** Bind the shared Files Git surface to the KB boundary, with a private store. */
export function useKnowledgebaseGit(source: ReadOnlyFileWorkspaceSource, revision: number, active: boolean): FileGitSource {
  const current = useRef(source)
  current.current = source
  const [writable, setWritable] = useState(false)
  const git = useMemo(() => {
    const endpoint = '/api/knowledgebase/git'
    let store: FileGitSource['store']
    const get = async (op: string, params: Record<string, unknown> = {}) => (await api.get(endpoint, { params: { op, ...params } })).data
    const gitApi: FileGitSource['api'] = {
      status: async () => {
        const data = await get('status')
        setWritable(!!data.writable)
        return (data.repos ?? []) as GitRepo[]
      },
      act: async (_workspace: string, _repo: string, action: GitAction) => {
        try {
          const data = (await api.post(endpoint, { ...action, request_id: crypto.randomUUID().replaceAll('-', '_') })).data
          return data.repo ?? null
        } finally { store.getState().openPanel(null); current.current.refresh() }
      },
      diff: async (_workspace, _repo, file) => get('diff', { file }),
      log: async (_workspace, _repo, file) => (await get('log', { file })).commits ?? [],
      branches: async () => (await get('branches')).branches ?? [],
      stashes: async () => (await get('stashes')).stashes ?? [],
      blame: async (_workspace, _repo, file) => get('blame', { file }),
      show: async (_workspace, _repo, file, commit) => get('show', { file, commit }),
    }
    store = createWorkspaceGitStore(gitApi)
    return {
      api: gitApi,
      store,
      openFile: (path: string) => current.current.openFile(path),
      readFile: async (path: string) => (await api.get('/api/knowledgebase/read', {params:{path}})).data.content,
      remoteAction: (op: 'pull' | 'push'): GitAction => ({ op }),
    }
  }, [])
  useEffect(() => {
    if (!active) { git.store.getState().clear(); return }
    // FileWorkspacePane refreshes status on mount and tree revisions.
  }, [active, revision, git])
  useEffect(() => () => git.store.getState().clear(), [git])
  return { ...git, writable }
}
