import { X } from 'lucide-react'
import type { WorkFilesView } from './useWorkFilesView'

export type CodePaneTab = 'changes' | 'files' | 'terminal'

/** Which tab a pane state belongs to: review views are Changes, browsing is Files. */
export function codePaneTab(view: string, filesView: WorkFilesView): CodePaneTab | null {
  if (view === 'shell') return 'terminal'
  if (view !== 'files') return null
  return filesView.kind === 'browse' || filesView.kind === 'folder' ? 'files' : 'changes'
}

/** Changes · Files · Terminal in one pane (owner, 20 Nov), like a desktop code app.
 * Each tab opens a view that already exists; nothing new is loaded here. */
export function CodePaneTabs({ active, showTerminal, onSelect, onClose }: { active: CodePaneTab; showTerminal: boolean; onSelect: (tab: CodePaneTab) => void; onClose?: () => void }) {
  const tabs: Array<{ id: CodePaneTab; label: string }> = [
    { id: 'changes', label: 'Changes' },
    { id: 'files', label: 'Files' },
    ...(showTerminal ? [{ id: 'terminal' as const, label: 'Terminal' }] : []),
  ]
  return <div role="tablist" aria-label="Code pane" className="flex shrink-0 items-stretch gap-1 border-b border-border bg-background px-2">
    {tabs.map(tab => <button key={tab.id} type="button" role="tab" aria-selected={active === tab.id}
      onClick={() => { if (active !== tab.id) onSelect(tab.id) }}
      className={`border-b-2 px-2.5 py-2 text-sm font-medium transition-colors ${active === tab.id ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>
      {tab.label}
    </button>)}
    {/* The pane's way out sits with its tabs, so the header below keeps a single back arrow (owner, 20 Nov). */}
    {onClose && <button type="button" onClick={onClose} aria-label="Close" title="Back to the project menu" className="my-auto ml-auto flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><X className="h-4 w-4" /></button>}
  </div>
}

/** Ctrl+` (the key left of 1), the code-editor shortcut for the terminal. */
export function isTerminalShortcut(event: Pick<KeyboardEvent, 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey' | 'code'>): boolean {
  return event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey && event.code === 'Backquote'
}
