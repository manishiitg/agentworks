import { useState } from 'react'
import { ArrowRight, Check, Copy, FileText, Folder, UsersRound } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'
import { useAuthStore } from '../../stores/useAuthStore'
import { visibleProductSurfaceIDs } from '../productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { copyToClipboard } from '../../utils/textUtils'

function KnowledgeMap() {
  return <svg viewBox="0 0 260 120" className="h-36 w-full" aria-hidden="true">
    <g fill="none" stroke="currentColor" strokeWidth="1.5" className="text-border"><path d="M97 60 59 27M97 60 54 94M160 60 202 26M160 60 205 94" /></g>
    <g fill="currentColor" className="text-muted"><rect x="11" y="11" width="67" height="30" rx="8" /><rect x="9" y="79" width="70" height="30" rx="8" /><rect x="178" y="11" width="72" height="30" rx="8" /><rect x="180" y="79" width="69" height="30" rx="8" /></g>
    <rect x="90" y="42" width="78" height="36" rx="10" fill="currentColor" className="text-purple-500/15" />
    <g fill="currentColor" fontSize="11" textAnchor="middle" className="text-muted-foreground"><text x="44" y="30">People</text><text x="44" y="98">Plans</text><text x="214" y="30">Projects</text><text x="214" y="98">Research</text></g>
    <text x="129" y="64" textAnchor="middle" fill="currentColor" fontSize="12" fontWeight="600" className="text-purple-600 dark:text-purple-300">Brain</text>
  </svg>
}

function DuplicateNotes() {
  return <div aria-hidden="true" className="relative mx-auto h-36 w-full max-w-64">
    <div className="absolute left-2 top-5 h-24 w-24 -rotate-6 rounded-lg border border-border bg-background p-3"><FileText className="h-4 w-4 text-muted-foreground" /><span className="mt-2 block text-[10px] text-muted-foreground">Team guide</span><span className="mt-2 block h-1 w-12 rounded bg-muted" /></div>
    <div className="absolute left-8 top-8 h-24 w-24 rotate-3 rounded-lg border border-border bg-background p-3"><FileText className="h-4 w-4 text-muted-foreground" /><span className="mt-2 block text-[10px] text-muted-foreground">Team guide</span><span className="mt-2 block h-1 w-12 rounded bg-muted" /></div>
    <ArrowRight className="absolute left-[54%] top-16 h-4 w-4 text-muted-foreground" />
    <div className="absolute right-0 top-8 flex h-24 w-[30%] flex-col items-center justify-center gap-2 rounded-lg border border-border bg-background text-center"><Check className="h-5 w-5 text-emerald-600 dark:text-emerald-400" /><span className="text-[10px] leading-4 text-muted-foreground">One clear<br />version</span></div>
  </div>
}

function TopicFolders() {
  return <div aria-hidden="true" className="mx-auto flex h-36 w-full max-w-60 flex-col justify-center gap-2">
    {['Projects', 'Team guides', 'Research'].map((name, index) => <div key={name} className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2"><Folder className="h-4 w-4 text-amber-600 dark:text-amber-400" /><span className="flex-1 text-xs text-muted-foreground">{name}</span><span className="text-[10px] text-muted-foreground/60">{[4, 3, 6][index]} notes</span></div>)}
  </div>
}

const examples = [
  { id: 'map', title: 'Map your knowledge', description: 'See how people, projects and ideas connect.', visual: KnowledgeMap, request: 'Create a visual map of how the topics in Brain connect, with links to the source notes.' },
  { id: 'duplicates', title: 'Find duplicate notes', description: 'Spot repeated information and decide what to combine.', visual: DuplicateNotes, request: 'Find duplicate or similar notes in Brain. Suggest what to combine and which version to keep. Ask me before changing or deleting anything.' },
  { id: 'topics', title: 'Group notes by topic', description: 'Give scattered notes a clear home and useful names.', visual: TopicFolders, request: 'Suggest folders and clearer names for my Brain notes. Show me the plan before moving or renaming anything.' },
]

/** Instructions only. Creation and configuration happen in the person's Crew. */
export function KnowledgebaseUseCrew() {
  const user = useAuthStore(state => state.user)
  const allowed = visibleProductSurfaceIDs(user?.allowed_products).includes('work')
  const [copied, setCopied] = useState<string | null>(null)
  const [copyError, setCopyError] = useState(false)
  return <WorkspacePageContent as="section" aria-label="Use Brain in a Crew" className="max-w-5xl space-y-6">
    <div className="space-y-2">
      <h2 className="text-lg font-semibold">Organize your Brain with a Crew</h2>
      <p className="max-w-2xl text-sm text-muted-foreground">Turn scattered notes into useful knowledge. Here are a few things you can ask your Crew to do.</p>
    </div>
    <ul aria-label="Ways to organize Brain" className="grid gap-4 md:grid-cols-3">
      {examples.map(({ id, title, description, visual: Visual, request }) => <li key={id} className="flex min-w-0 flex-col overflow-hidden rounded-xl border border-border">
        <div className="border-b border-border bg-muted/20 px-3 py-2"><Visual /></div>
        <div className="flex flex-1 flex-col gap-3 p-4">
          <div className="space-y-1.5"><h3 className="text-sm font-semibold">{title}</h3><p className="text-xs leading-5 text-muted-foreground">{description}</p></div>
          <blockquote className="mt-1 flex-1 border-l-2 border-border pl-3 text-xs leading-5 text-muted-foreground">“{request}”</blockquote>
          <Button variant="ghost" size="sm" className="w-fit px-0 text-xs" aria-label={'Copy request: ' + title} onClick={async () => {
            const ok = await copyToClipboard(request)
            setCopied(ok ? id : null); setCopyError(!ok)
          }}>{copied === id ? <Check aria-hidden="true" className="mr-2 h-3.5 w-3.5" /> : <Copy aria-hidden="true" className="mr-2 h-3.5 w-3.5" />}{copied === id ? 'Copied' : 'Copy request'}</Button>
        </div>
      </li>)}
    </ul>
    <p role="status" aria-live="polite" className="text-xs text-muted-foreground">{copyError ? 'Could not copy. Select the request above and copy it manually.' : copied ? 'Request copied. Paste it into your Crew when you are ready.' : 'These are examples of work a Crew can do with your Brain notes.'}</p>
    <div className="flex flex-wrap items-center gap-3">
      {allowed ? <Button variant="outline" size="sm" onClick={() => {
        if (visibleProductSurfaceIDs(useAuthStore.getState().user?.allowed_products).includes('work')) openProductWorkspace('work')
      }}>Open Crew<ArrowRight aria-hidden="true" className="ml-2 h-3.5 w-3.5" /></Button> : <p className="text-sm text-muted-foreground">Ask an administrator for Crew access to organize Brain.</p>}
      <p className="text-xs text-muted-foreground">Create or open your Crew, then paste a request there.</p>
    </div>
    <details className="rounded-lg border border-border p-4">
      <summary className="w-fit cursor-pointer text-sm font-medium">How to set this up</summary>
      <ol className="mt-4 space-y-3 text-xs leading-5 text-muted-foreground">
        <li className="flex gap-2"><span className="font-medium text-foreground">1.</span><span>Choose <strong className="font-medium text-foreground">New project → Crew</strong>, or open a Crew you already use.</span></li>
        <li className="flex gap-2"><span className="font-medium text-foreground">2.</span><span>In the Crew, open <strong className="font-medium text-foreground">Connections → Add connection → Brain</strong>. Read &amp; write is the default; choose Read only for reviewing notes without edits. Only the Crew owner can change this setting.</span></li>
        <li className="flex gap-2"><span className="font-medium text-foreground">3.</span><span>Tell the Crew which notes to work with. For regular cleanup, create a function and schedule inside the Crew.</span></li>
      </ol>
      <p className="mt-4 flex items-start gap-2 border-t border-border pt-3 text-xs leading-5 text-muted-foreground"><UsersRound aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0" />Your Crew uses existing Brain folder permissions. Setting up a Crew does not share those folders with anyone else.</p>
    </details>
  </WorkspacePageContent>
}
