import { lazy, Suspense, useContext, useEffect, useId, useRef, useState } from 'react'
import { CircleHelp, X } from 'lucide-react'
import { AskAIButton } from './AskAIButton'
import { getWorkspacePanelGuide } from './workspacePanelGuides'
import { WorkspacePanelGuideContext } from './WorkspacePanelGuideContext'
import { integrationHowToTopic } from './integrationHowToTopics'
import type { PanelGuideAsk } from './workspacePanelGuideAsk'

const IntegrationHowToGuide = lazy(() => import('./IntegrationHowToGuide').then(module => ({ default: module.IntegrationHowToGuide })))

/** A small, on-demand explanation attached to a workspace panel header. */
export function WorkspacePanelGuideButton({ topic, ask }: { topic: string; ask?: PanelGuideAsk }) {
  const surface = useContext(WorkspacePanelGuideContext)
  const guide = getWorkspacePanelGuide(topic, surface)
  const howToTopic = integrationHowToTopic(topic)
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const dialogId = useId()

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      buttonRef.current?.focus()
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const close = () => {
    setOpen(false)
    buttonRef.current?.focus()
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen(value => !value)}
        aria-label={`Walkthrough: ${guide.title}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? dialogId : undefined}
        title={`Walkthrough: ${guide.title}`}
        className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary"
      >
        <CircleHelp className="h-3.5 w-3.5" />
      </button>
      {open && (
        <div
          id={dialogId}
          role="dialog"
          aria-label={`${guide.title} walkthrough`}
          className={`absolute right-0 top-full z-50 mt-2 overflow-y-auto rounded-xl border border-border bg-popover text-popover-foreground shadow-xl ${howToTopic ? 'max-h-[min(42rem,calc(100vh-5rem))] w-[min(40rem,calc(100vw-1.5rem))] p-5' : 'max-h-[min(32rem,calc(100vh-5rem))] w-[min(20rem,calc(100vw-1.5rem))] p-4'}`}
        >
          <p className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-primary">{guide.surface === 'crew' ? 'Crew' : 'AgentWorks'} · {guide.group}</p>
          <div className="flex items-start justify-between gap-3">
            <h3 className="text-sm font-semibold">About {guide.title}</h3>
            <button type="button" onClick={close} aria-label="Close panel walkthrough" className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"><X className="h-4 w-4" /></button>
          </div>
          <p className="mt-2 text-sm leading-5 text-muted-foreground">{guide.purpose}</p>
          <p className="mt-3 text-xs font-semibold uppercase tracking-wide text-foreground">How to use it</p>
          <p className="mt-1 text-sm leading-5 text-muted-foreground">{guide.howTo}</p>
          {howToTopic && <Suspense fallback={<p className="mt-4 text-sm text-muted-foreground">Loading help…</p>}>
            <IntegrationHowToGuide topic={howToTopic} scopeNoun={surface === 'crew' ? 'project' : 'workflow'} />
          </Suspense>}
          {guide.steps && (
            <ol className="mt-3 list-decimal space-y-2 pl-5 text-sm leading-5 text-muted-foreground" aria-label="Setup steps">
              {guide.steps.map(step => <li key={step} className="pl-0.5">{step}</li>)}
            </ol>
          )}
          {ask ? (
            <div className="mt-4 flex items-center gap-2">
              <button type="button" onClick={close} className="rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90">Got it</button>
              <AskAIButton workspacePath={ask.workspacePath} message={ask.message} onAsk={ask.onAsk} label={ask.label ?? 'Ask AI'} />
            </div>
          ) : (
            <button type="button" onClick={close} className="mt-4 rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90">Got it</button>
          )}
        </div>
      )}
    </div>
  )
}
