import { useEffect, useState, type ReactNode } from 'react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccess } from '../../services/knowledgebaseApi'
import { FileWorkspacePane } from '../../components/FileWorkspacePane'
import { TooltipProvider } from '../../components/ui/tooltip'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'
import { KnowledgebaseAccessPanel } from './KnowledgebaseAccessPanel'
import { KnowledgebaseSecrets } from './KnowledgebaseSecrets'
import { KnowledgebaseFolderTree } from './KnowledgebaseFolderTree'
import { useKnowledgebaseFiles } from './useKnowledgebaseFiles'
import { KnowledgebaseSchedules } from './KnowledgebaseSchedules'
import { ArrowRight, BookOpen, FileText, Plug, ShieldCheck, Users } from 'lucide-react'

export type KnowledgebaseView = 'library' | 'access' | 'models' | 'secrets' | 'schedules' | 'backup' | 'crew'
function KnowledgebaseFiles({ folder, onFolder, onAsk, onGitAsk, onConnect, onRefresh, onOpenAccess, onUseCrew, revision, active }: { onUseCrew?: () => void; folder: string; onGitAsk?: (message: string) => void | Promise<unknown>; onConnect?: () => void; onRefresh?: () => void; onOpenAccess?: () => void; active: boolean; onFolder: (path: string) => void; onAsk?: () => void; revision: number }) {
  const source = useKnowledgebaseFiles(revision, active, onFolder, folder)
  // Git backup runs with plain git in Brain's folder, from the Brain chat (PLAT-633), not a panel here.
  return <TooltipProvider><FileWorkspacePane title="Brain" source={onRefresh ? { ...source, refresh: onRefresh } : source} testId="knowledgebase-files" onAsk={onGitAsk} hideAddToChat hideRootActions
    treeHeader={<p className="text-xs text-muted-foreground">Knowledge your agents can reuse.</p>}
    emptyState={<div className="flex min-h-64 flex-col items-center justify-center gap-3 px-5 py-10 text-center sm:py-14">
      <BookOpen className="h-8 w-8 text-muted-foreground" aria-hidden="true" />
      <h2 className="text-base font-semibold">{onConnect ? 'Knowledge your agents can share' : 'No knowledge available yet'}</h2>
      <p className="max-w-md text-sm leading-6 text-muted-foreground">{onConnect ? 'Save notes and instructions once. Your connected AI tools can add to them and reuse them in later work.' : 'Files and folders shared with you appear here.'}</p>
      {onConnect && <>
        <ol aria-label="How Brain works" className="my-3 flex w-full max-w-lg flex-col items-center justify-center gap-3 text-xs text-muted-foreground sm:flex-row sm:gap-4">
          <li className="flex items-center gap-2"><Plug className="h-4 w-4 shrink-0" aria-hidden="true" /><span>Connect AI tools</span></li>
          <li className="flex items-center gap-2"><ArrowRight className="mr-2 hidden h-3 w-3 shrink-0 sm:block" aria-hidden="true" /><FileText className="h-4 w-4 shrink-0" aria-hidden="true" /><span>Save useful knowledge</span></li>
          <li className="flex items-center gap-2"><ArrowRight className="mr-2 hidden h-3 w-3 shrink-0 sm:block" aria-hidden="true" /><BookOpen className="h-4 w-4 shrink-0" aria-hidden="true" /><span>Reuse across agents</span></li>
        </ol>
        <div className="flex flex-wrap justify-center gap-2"><button type="button" onClick={onUseCrew ?? onConnect} className="inline-flex min-h-9 items-center gap-2 rounded-md bg-foreground px-3 py-2 text-xs font-medium text-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{onUseCrew ? <Users className="h-3.5 w-3.5" aria-hidden="true" /> : <Plug className="h-3.5 w-3.5" aria-hidden="true" />}{onUseCrew ? 'Use in a Crew' : 'Connect AI tools'}</button>{onUseCrew && <button type="button" onClick={onConnect} className="min-h-9 rounded-md border border-border px-3 py-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Connect an AI app</button>}</div>
        <p className="mt-1 text-xs text-muted-foreground">You control who can open or edit each folder.</p>
      </>}
    </div>}

    headerAction={onOpenAccess && <button type="button" onClick={onOpenAccess} className="inline-flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"><ShieldCheck className="h-3.5 w-3.5" aria-hidden="true" />Folder access</button>} /></TooltipProvider>
}
export function KnowledgebaseWorkspacePane({ view, folder, onFolder, onAsk, revision, modelSettings, onGitAsk, onConnect, onRefresh, onOpenAccess, onUseCrew, backup, crew, onPrepared = () => {}, isAdmin = false }: { onUseCrew?: () => void; crew?: ReactNode; onPrepared?: () => void; isAdmin?: boolean; onGitAsk?: (message: string) => void | Promise<unknown>; onConnect?: () => void; onRefresh?: () => void; onOpenAccess?: () => void; backup?: ReactNode; view: KnowledgebaseView; folder: string; onFolder: (path: string) => void; onAsk?: () => void; revision: number; modelSettings?: ReactNode }) {
  const [access, setAccess] = useState<KnowledgeAccess | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    if (view !== 'access') return
    const controller = new AbortController(); setAccess(current => current?.folder_path === folder ? current : null); setError('')
    void knowledgebaseApi.access(folder, controller.signal).then(result => { if (!controller.signal.aborted) setAccess(result) })
      .catch(failure => { if (!controller.signal.aborted) setError(knowledgebaseError(failure)) })
    return () => controller.abort()
  }, [view, folder, revision])
  return <div className="flex h-full min-h-0 flex-col">
    <div className="min-h-0 flex-1" hidden={view !== 'library'}><KnowledgebaseFiles folder={folder} active={view === 'library'} onFolder={onFolder} onAsk={onAsk} onGitAsk={onGitAsk} onConnect={onConnect} onRefresh={onRefresh} onOpenAccess={onOpenAccess} onUseCrew={onUseCrew} revision={revision} /></div>
    {/* Brain's own secrets, not Vault's platform secrets; backup names one with pat_secret. */}
    {view === 'crew' && <div className="min-h-0 flex-1 overflow-y-auto">{crew}</div>}
    {view === 'backup' && <div className="min-h-0 flex-1 overflow-y-auto">{backup}</div>}
    {view === 'secrets' && <div className="min-h-0 flex-1 overflow-y-auto"><KnowledgebaseSecrets /></div>}
    {view === 'schedules' && <div className="min-h-0 flex-1 overflow-y-auto"><KnowledgebaseSchedules /></div>}
    {view === 'models' && <WorkspacePageContent className="min-h-0 flex-1 space-y-4 overflow-y-auto"><SettingsCardLayout unboxed>{modelSettings}</SettingsCardLayout></WorkspacePageContent>}
    {view === 'access' && <div className="flex min-h-0 flex-1"><aside className="w-48 shrink-0 overflow-y-auto border-r border-border"><KnowledgebaseFolderTree selected={folder} onSelect={onFolder} revision={revision} /></aside><div className="min-w-0 flex-1 overflow-y-auto">{error && <p role="alert" className="p-4 text-sm text-destructive">{error}</p>}{access ? <KnowledgebaseAccessPanel key={access.folder_path} access={access} onPrepared={onPrepared} isAdmin={isAdmin} onFolder={onFolder} /> : !error ? <p className="p-4 text-sm text-muted-foreground">Loading access…</p> : null}</div></div>}
  </div>
}
