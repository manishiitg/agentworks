import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import '../../src/App.css'
import { ChatFileReview } from '../../src/components/workspace/ChatFileReview'
import { ThemeContext } from '../../src/contexts/ThemeContext'
import api from '../../src/services/api'
import { workspaceGitApi } from '../../src/services/workspaceGit'

// Real review components, synthetic comparisons only; no server, chat or file writes.
const options = new URLSearchParams(location.search)
const count = Number(options.get('count') || 2)
const changes = Array.from({ length: count }, (_, index) => ({ path: `file-${index}.txt`, status: 'M', added: 1, removed: 1 }))
const diff = (file: string) => `@@ -1 +1 @@\n-old ${file}\n+new ${file}\n`
api.get = (async (_url: string, config: { params: { file?: string; op?: string } }) => ({ data: config.params.op === 'snapshots' ? { recent_repos: options.has('worktree') ? ['app-wt'] : [] } : {
  history: { replies: [], reviews: [], available: true, last_snapshot_id: 'last', baseline_snapshot_id: 'start', recent_repos: options.has('worktree') ? ['app-wt'] : [] },
  total_changes: count,
  comparison: { changes, diff: config.params.file ? diff(config.params.file) : '', note: '', snapshot: { status: 'complete' } },
} })) as typeof api.get
workspaceGitApi.status = async () => options.has('worktree') ? [
  { root: 'app', branch: 'main', ahead: 0, behind: 0, files: [] },
  { root: 'app-wt', branch: 'feature', ahead: 0, behind: 0, files: [] },
] : options.has('git') ? [{ root: 'app', branch: 'feature', ahead: 0, behind: 0, files: changes.map(file => ({ path: file.path, status: 'modified' as const })) }] : []
workspaceGitApi.diff = async (_workspace, _repo, file) => ({ diff: diff(file), truncated: false })
workspaceGitApi.branches = async () => [{ name: 'main' }]
workspaceGitApi.branchCompare = async (_workspace, repo) => ({ branch: repo === 'app-wt' ? 'feature' : 'main', base: 'main', unpushed: 0, files: [] })
createRoot(document.getElementById('root')!).render(<ThemeContext.Provider value={{ theme: 'dark', toggleTheme: () => {}, setTheme: () => {} }}>
  <main className="h-dvh min-h-0 overflow-hidden bg-background text-foreground"><ChatFileReview workspacePath="Crew/example" sessionId="synthetic" tabId="example" onBrowse={() => {}} /></main>
</ThemeContext.Provider>)
