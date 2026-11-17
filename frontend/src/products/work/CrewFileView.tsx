import { lazy, Suspense, useEffect, useState } from 'react'
import api from '../../services/api'
import { ArrowLeft } from 'lucide-react'
import { PageCard, RowButton } from '../../components/automation/AutomationKit'

const Code = lazy(() => import('../../components/ui/SyntaxHighlightedCode'))
const languageOf = (file: string) => file.endsWith('.py') ? 'python' : file.endsWith('.json') ? 'json' : file.endsWith('.md') ? 'markdown' : 'text'

/** A workflow's file, read only and nothing else: the back arrow above leaves it. */
export function CrewFileView({ workspacePath, file, onBack, onAsk }: {
  workspacePath: string
  file: string
  /** Closes the file and returns to the screen it was opened from. */
  onBack?: () => void
  /** Opens the Crew chat with a message about this file, so the agent can ask what to change. */
  onAsk?: () => void
}) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let current = true
    setContent(null); setError('')
    api.get('/api/crew-script-file', { params: { workspace_path: workspacePath, file } }).then(response => response.data)
      .then(response => {
        if (!response.success || !response.data) throw new Error(response.error || 'Could not open the file.')
        if (current) setContent(String(response.data.content ?? ''))
      })
      .catch(cause => { if (current) setError(cause instanceof Error ? cause.message : 'Could not open the file.') })
    return () => { current = false }
  }, [workspacePath, file])
  const isDark = document.documentElement.classList.contains('dark')
  return <PageCard title={file} testId="crew-file-view" action={onBack || onAsk ? <span className="flex items-center gap-2">
    {onAsk && <RowButton title="Ask your agent about this file" onClick={onAsk}>Ask</RowButton>}
    {onBack && <RowButton title="Back" onClick={onBack}><span className="flex items-center gap-1"><ArrowLeft className="h-3 w-3" aria-hidden="true" />Back</span></RowButton>}
  </span> : undefined}>
    {error ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{error}</p>
      : content === null ? <p className="px-4 py-3 text-sm text-muted-foreground">Loading…</p>
      : <div className="max-h-[75vh] overflow-auto"><Suspense fallback={<p className="px-4 py-3 text-sm text-muted-foreground">Loading…</p>}><Code code={content} language={languageOf(file)} isDark={isDark} showLineNumbers wrapLongLines /></Suspense></div>}
  </PageCard>
}
