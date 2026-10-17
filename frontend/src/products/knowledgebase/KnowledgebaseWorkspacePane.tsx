import { useEffect, useState, type ReactNode } from 'react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccess } from '../../services/knowledgebaseApi'
import { FileWorkspacePane } from '../../components/FileWorkspacePane'
import { AskAIButton } from '../../components/workflow/AskAIButton'
import { TooltipProvider } from '../../components/ui/tooltip'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { KnowledgebaseAccessPanel } from './KnowledgebaseAccessPanel'
import { KnowledgebaseSecrets } from './KnowledgebaseSecrets'
import { KnowledgebaseFolderTree } from './KnowledgebaseFolderTree'
import { useKnowledgebaseFiles } from './useKnowledgebaseFiles'
import { KnowledgebaseSchedules } from './KnowledgebaseSchedules'
import { BookOpen, Plug } from 'lucide-react'

export type KnowledgebaseView = 'library' | 'access' | 'models' | 'secrets' | 'schedules'
function KnowledgebaseFiles({ folder, onFolder, onAsk, onGitAsk, onConnect, revision, active }: { folder: string; onGitAsk?: (message: string) => void | Promise<unknown>; onConnect?: () => void; active: boolean; onFolder: (path: string) => void; onAsk?: () => void; revision: number }) {
  const source = useKnowledgebaseFiles(revision, active, onFolder, folder)
  // Git backup runs with plain git in Brain's folder, from the Brain chat (PLAT-633), not a panel here.
  return <TooltipProvider><FileWorkspacePane title="Brain" source={source} testId="knowledgebase-files" onAsk={onGitAsk} hideAddToChat hideRootActions
    treeHeader={<p className="text-xs text-muted-foreground">Knowledge your agents can reuse.</p>}
    emptyState={<div className="flex min-h-64 flex-col items-center justify-center gap-3 px-6 py-12 text-center"><BookOpen className="h-8 w-8 text-muted-foreground" aria-hidden="true" /><h2 className="text-sm font-medium">No knowledge available yet</h2><p className="max-w-xs text-xs leading-5 text-muted-foreground">{onConnect ? 'Connect your AI tools to add and reuse knowledge.' : 'Files and folders shared with you appear here.'}</p>{onConnect && <button type="button" onClick={onConnect} className="mt-1 inline-flex items-center gap-2 rounded-md bg-foreground px-3 py-2 text-xs font-medium text-background"><Plug className="h-3.5 w-3.5" />Connect AI tools</button>}</div>}
    headerAction={onAsk && <AskAIButton workspacePath={null} onAsk={onAsk} message="Inspect the selected folder's access." label="Folder access" />} /></TooltipProvider>
}
export function KnowledgebaseWorkspacePane({ view, folder, onFolder, onAsk, revision, modelSettings, onGitAsk, onConnect }: { onGitAsk?: (message: string) => void | Promise<unknown>; onConnect?: () => void; view: KnowledgebaseView; folder: string; onFolder: (path: string) => void; onAsk?: () => void; revision: number; modelSettings?: ReactNode }) {
  const [access, setAccess] = useState<KnowledgeAccess | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    if (view !== 'access') return
    const controller = new AbortController(); setAccess(null); setError('')
    void knowledgebaseApi.access(folder, controller.signal).then(result => { if (!controller.signal.aborted) setAccess(result) })
      .catch(failure => { if (!controller.signal.aborted) setError(knowledgebaseError(failure)) })
    return () => controller.abort()
  }, [view, folder, revision])
  return <div className="flex h-full min-h-0 flex-col">
    <div className="min-h-0 flex-1" hidden={view !== 'library'}><KnowledgebaseFiles folder={folder} active={view === 'library'} onFolder={onFolder} onAsk={onAsk} onGitAsk={onGitAsk} onConnect={onConnect} revision={revision} /></div>
    {/* Brain's own secrets, not Vault's platform secrets; backup names one with pat_secret. */}
    {view === 'secrets' && <div className="min-h-0 flex-1 overflow-y-auto"><KnowledgebaseSecrets /></div>}
    {view === 'schedules' && <div className="min-h-0 flex-1 overflow-y-auto"><KnowledgebaseSchedules /></div>}
    {view === 'models' && <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4"><SettingsCardLayout unboxed>{modelSettings}</SettingsCardLayout></div>}
    {view === 'access' && <div className="flex min-h-0 flex-1"><aside className="w-48 shrink-0 overflow-y-auto border-r border-border"><KnowledgebaseFolderTree selected={folder} onSelect={onFolder} revision={revision} /></aside><div className="min-w-0 flex-1 overflow-y-auto">{error ? <p role="alert" className="p-4 text-sm text-destructive">{error}</p> : access ? <KnowledgebaseAccessPanel access={access} onAsk={onAsk ?? (() => {})} onFolder={onFolder} /> : <p className="p-4 text-sm text-muted-foreground">Loading access…</p>}</div></div>}
  </div>
}
