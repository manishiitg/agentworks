import { useEffect, useId, useRef, useState } from 'react'
import { CircleHelp, X } from 'lucide-react'
import { panelBackAction, useWorkspacePanelNavigationStore, useWorkspacePanelScope } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspacePanelGuideContent } from '../workflow/WorkspacePanelGuideButton'
import { WorkspacePanelAssistantButton } from './WorkspacePanelNavigation'
import type { WorkspacePanelSurface } from '../workflow/workspacePanelGuides'

/** One shared entry point for page instructions and a contextual chat draft. */
export function WorkspacePanelHelpButton({ workspacePath, view, contained = false, fallbackGuide }: { workspacePath?: string | null; view?: string; contained?: boolean; fallbackGuide?: { topic: string; surface: WorkspacePanelSurface } }) {
  const scope = useWorkspacePanelScope(workspacePath, view)
  const registeredGuide = useWorkspacePanelNavigationStore(state => panelBackAction(state.guides, scope))
  const guide = registeredGuide ?? fallbackGuide
  const ask = useWorkspacePanelNavigationStore(state => panelBackAction(state.assistants, scope))
  const [open, setOpen] = useState(false)
  const container = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const dialog = useRef<HTMLDivElement>(null)
  const id = useId()
  useEffect(() => { setOpen(false) }, [scope, guide?.topic, guide?.surface, ask?.message])
  useEffect(() => {
    if (!open) return
    dialog.current?.querySelector<HTMLButtonElement>('[aria-label="Close help"]')?.focus()
    const outside = (event: PointerEvent) => { if (!container.current?.contains(event.target as Node)) setOpen(false) }
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false); trigger.current?.focus()
    }
    document.addEventListener('pointerdown', outside)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', escape) }
  }, [open])
  if (!guide && !ask) return null
  return <div ref={container} className={contained ? 'shrink-0' : 'relative shrink-0'}>
    <button ref={trigger} type="button" aria-label="Help with this page" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined} title="Help with this page" onClick={() => setOpen(value => !value)} className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <CircleHelp className="h-3.5 w-3.5" aria-hidden="true" />Help
    </button>
    {open && <div ref={dialog} id={id} role="dialog" aria-label="Page help" className={`absolute z-50 overflow-y-auto rounded-xl border border-border bg-popover p-2 text-popover-foreground shadow-xl ${contained ? 'right-2 top-12 max-h-[calc(100%-4rem)] w-[min(36rem,calc(100%-1rem))]' : `right-0 top-full mt-2 max-h-[calc(100dvh-8rem)] ${guide ? 'w-[min(36rem,calc(100vw-1.5rem))]' : 'w-[min(17rem,calc(100vw-1.5rem))]'}`}`}>
      <div className="mb-1 flex items-center justify-between gap-3 px-3 py-1.5"><p className="text-sm font-semibold">{guide ? 'Page guide' : 'Help'}</p><button type="button" aria-label="Close help" onClick={() => { setOpen(false); trigger.current?.focus() }} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><X className="h-3.5 w-3.5" /></button></div>
      {guide && <section aria-label="Page guide" className="px-3 pb-4 pt-2"><WorkspacePanelGuideContent topic={guide.topic} surface={guide.surface} /></section>}
      {ask && <WorkspacePanelAssistantButton panelScope={scope} inMenu onDraftOpened={() => setOpen(false)} />}
    </div>}
  </div>
}
