import { useEffect, useState, type ReactNode } from 'react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccess } from '../../services/knowledgebaseApi'
import { FileWorkspacePane } from '../../components/FileWorkspacePane'
import { AskAIButton } from '../../components/workflow/AskAIButton'
import { TooltipProvider } from '../../components/ui/tooltip'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { KnowledgebaseAccessPanel } from './KnowledgebaseAccessPanel'
import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { KnowledgebaseFolderTree } from './KnowledgebaseFolderTree'
import { useKnowledgebaseFiles } from './useKnowledgebaseFiles'

export type KnowledgebaseView = 'library' | 'access' | 'models' | 'secrets'
function KnowledgebaseFiles({ onFolder, onAsk, onGitAsk, revision, active }: { onGitAsk?: (message: string) => void | Promise<unknown>; active: boolean; onFolder: (path: string) => void; onAsk: () => void; revision: number }) {
  const source = useKnowledgebaseFiles(revision, active, onFolder)
  // Git backup runs with plain git in Brain's folder, from the Brain chat (PLAT-633), not a panel here.
  return <TooltipProvider><FileWorkspacePane title="Brain" source={source} testId="knowledgebase-files" onAsk={onGitAsk} hideAddToChat hideRootActions
    headerAction={<AskAIButton workspacePath={null} onAsk={onAsk} message="Inspect the selected folder's access." label="Folder access" />} /></TooltipProvider>
}
export function KnowledgebaseWorkspacePane({ view, folder, onFolder, onAsk, revision, modelSettings, onGitAsk }: { onGitAsk?: (message: string) => void | Promise<unknown>; view: KnowledgebaseView; folder: string; onFolder: (path: string) => void; onAsk: () => void; revision: number; modelSettings?: ReactNode }) {
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
    <div className="min-h-0 flex-1" hidden={view !== 'library'}><KnowledgebaseFiles active={view === 'library'} onFolder={onFolder} onAsk={onAsk} onGitAsk={onGitAsk} revision={revision} /></div>
    {/* The same platform secrets Vault manages; the Brain chat reads them (list_secrets) and backup names one with pat_secret. */}
    {view === 'secrets' && <div className="min-h-0 flex-1 overflow-y-auto p-4"><SecretSelectionSection mode="vault" selectedSecrets={[]} onSecretChange={() => {}} /></div>}
    {view === 'models' && <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4"><SettingsCardLayout unboxed>{modelSettings}</SettingsCardLayout></div>}
    {view === 'access' && <div className="flex min-h-0 flex-1"><aside className="w-48 shrink-0 overflow-y-auto border-r border-border"><KnowledgebaseFolderTree selected={folder} onSelect={onFolder} revision={revision} /></aside><div className="min-w-0 flex-1 overflow-y-auto">{error ? <p role="alert" className="p-4 text-sm text-destructive">{error}</p> : access ? <KnowledgebaseAccessPanel access={access} onAsk={onAsk} onFolder={onFolder} /> : <p className="p-4 text-sm text-muted-foreground">Loading access…</p>}</div></div>}
  </div>
}
