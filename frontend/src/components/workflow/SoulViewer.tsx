import { useCallback, useEffect, useState } from 'react'
import { Loader2, Target } from 'lucide-react'
import { agentApi } from '../../services/api'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { extractWorkflowGoalSections } from './soulSummaryUtils'

// Fired by the Pulse popup refresh button so goal content and module status stay aligned.
export const WORKFLOW_SOUL_REFRESH_EVENT = 'workflow-soul-refresh'

interface SoulViewerProps {
  workspacePath: string
  embedded?: boolean
  goalSummary?: boolean
}

// SoulViewer renders the workflow's north star (soul/soul.md — ## Objective +
// ## Success Criteria). soul.md stays markdown because framework health and
// runtime objective injection parse it directly.
export function SoulViewer({ workspacePath, embedded = false, goalSummary = false }: SoulViewerProps) {
  const [content, setContent] = useState('')
  const [exists, setExists] = useState<boolean | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!workspacePath) return
    setLoading(true)
    setError(null)
    try {
      const res = await agentApi.getBuilderDoc(workspacePath, 'soul')
      setExists(!!res.exists)
      setContent(res.content || '')
      if (!res.success && res.error) setError(res.error)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    const onRefresh = () => { void load() }
    window.addEventListener(WORKFLOW_SOUL_REFRESH_EVENT, onRefresh)
    return () => window.removeEventListener(WORKFLOW_SOUL_REFRESH_EVENT, onRefresh)
  }, [load])

  if (loading && !content) {
    if (goalSummary) {
      return (
        <div className="flex min-h-20 items-center justify-center gap-2 rounded-xl border bg-background text-xs text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading goal and success criteria…
        </div>
      )
    }
    return (
      <div className={`flex items-center justify-center gap-2 text-sm text-muted-foreground ${embedded ? 'min-h-40' : 'h-full'}`}>
        <Loader2 className="h-4 w-4 animate-spin" /> Loading soul…
      </div>
    )
  }

  if (error) {
    if (goalSummary) {
      return (
        <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
          Goal and success criteria could not be loaded: {error}
        </div>
      )
    }
    return (
      <div className={`flex items-center justify-center p-6 ${embedded ? 'min-h-40' : 'h-full'}`}>
        <div className="max-w-md rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
          {error}
        </div>
      </div>
    )
  }

  if (exists === false || !content.trim()) {
    if (goalSummary) {
      return (
        <div className="rounded-xl border bg-background p-4 text-xs text-muted-foreground">
          No workflow goal or success criteria yet. Run <code className="rounded bg-muted px-1">/setup-goals</code>.
        </div>
      )
    }
    return (
      <div className={`flex items-center justify-center p-6 text-center ${embedded ? 'min-h-40' : 'h-full'}`}>
        <div className="max-w-md text-sm text-muted-foreground">
          No soul yet — the workflow's north star. Run <code className="rounded bg-muted px-1">/setup-goals</code> to
          confirm the <code className="rounded bg-muted px-1">## Objective</code> and <code className="rounded bg-muted px-1">## Success Criteria</code>.
        </div>
      </div>
    )
  }

  if (goalSummary) {
    const summary = extractWorkflowGoalSections(content)
    return (
      <section className="rounded-xl border bg-background p-4 sm:p-5" aria-label="Workflow goal">
        <h2 className="mb-3 flex items-center gap-2 text-sm font-semibold"><Target className="h-4 w-4 text-sky-500" />What we’re working toward</h2>
        {summary.primaryGoals || summary.secondaryGoals ? (
          <div className="space-y-4">
            {summary.primaryGoals && <div><h3 className="mb-2 text-sm font-semibold text-sky-700 dark:text-sky-300">Primary goals</h3><MarkdownRenderer content={summary.primaryGoals} disablePathLinking /></div>}
            {summary.secondaryGoals && <div className={summary.primaryGoals ? 'border-t pt-4' : ''}><h3 className="mb-2 text-sm font-semibold">Secondary goals</h3><MarkdownRenderer content={summary.secondaryGoals} disablePathLinking /></div>}
            {summary.otherGoals && <div className="border-t pt-4"><h3 className="mb-2 text-sm font-medium text-muted-foreground">Additional goal context</h3><MarkdownRenderer content={summary.otherGoals} disablePathLinking /></div>}
          </div>
        ) : <MarkdownRenderer content={summary.goal || 'No outcome goals defined yet. Use /setup-goals.'} disablePathLinking />}
        {summary.acceptance && <details className="mt-4 border-t pt-3 text-xs"><summary className="cursor-pointer font-medium text-muted-foreground">Acceptance conditions</summary><div className="mt-3"><MarkdownRenderer content={summary.acceptance} disablePathLinking /></div></details>}
        {summary.boundaries && <details className="mt-3 border-t pt-3 text-xs"><summary className="cursor-pointer font-medium text-muted-foreground">What must stay true</summary><div className="mt-3"><MarkdownRenderer content={summary.boundaries} disablePathLinking /></div></details>}
      </section>
    )
  }

  return (
    <div className={embedded ? 'px-4 py-4 sm:px-5' : 'h-full overflow-y-auto px-6 py-5'}>
      <div className={embedded ? 'max-w-none' : 'mx-auto max-w-3xl'}>
        <MarkdownRenderer content={content} disablePathLinking />
      </div>
    </div>
  )
}

export default SoulViewer
