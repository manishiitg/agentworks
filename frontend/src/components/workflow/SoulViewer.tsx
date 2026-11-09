import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { agentApi } from '../../services/api'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

// Fired by the Pulse popup refresh button so goal content and module status stay aligned.
export const WORKFLOW_SOUL_REFRESH_EVENT = 'workflow-soul-refresh'

interface SoulViewerProps {
  workspacePath: string
  embedded?: boolean
}

// SoulViewer renders the workflow's north star (soul/soul.md — ## Objective +
// ## Success Criteria). soul.md stays markdown because framework health and
// runtime objective injection parse it directly.
export function SoulViewer({ workspacePath, embedded = false }: SoulViewerProps) {
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
    return (
      <div className={`flex items-center justify-center gap-2 text-sm text-muted-foreground ${embedded ? 'min-h-40' : 'h-full'}`}>
        <Loader2 className="h-4 w-4 animate-spin" /> Loading soul…
      </div>
    )
  }

  if (error) {
    return (
      <div className={`flex items-center justify-center p-6 ${embedded ? 'min-h-40' : 'h-full'}`}>
        <div className="max-w-md rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
          {error}
        </div>
      </div>
    )
  }

  if (exists === false || !content.trim()) {
    return (
      <div className={`flex items-center justify-center p-6 text-center ${embedded ? 'min-h-40' : 'h-full'}`}>
        <div className="max-w-md text-sm text-muted-foreground">
          No soul yet — the workflow's north star, with an <code className="rounded bg-muted px-1">## Objective</code> and <code className="rounded bg-muted px-1">## Success Criteria</code>.
        </div>
      </div>
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
