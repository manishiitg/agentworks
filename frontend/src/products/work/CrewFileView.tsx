import { lazy, Suspense, useEffect, useState } from 'react'
import api from '../../services/api'
import { PageCard } from '../../components/automation/AutomationKit'

const Code = lazy(() => import('../../components/ui/SyntaxHighlightedCode'))
const languageOf = (file: string) => file.endsWith('.py') ? 'python' : file.endsWith('.json') ? 'json' : file.endsWith('.md') ? 'markdown' : 'text'

/** A workflow's file, read only and nothing else: the back arrow above leaves it. */
export function CrewFileView({ workspacePath, file }: {
  workspacePath: string
  file: string
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
  return <PageCard title={file} testId="crew-file-view">
    {error ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{error}</p>
      : content === null ? <p className="px-4 py-3 text-sm text-muted-foreground">Loading…</p>
      : <div className="max-h-[75vh] overflow-auto"><Suspense fallback={<p className="px-4 py-3 text-sm text-muted-foreground">Loading…</p>}><Code code={content} language={languageOf(file)} isDark={isDark} showLineNumbers /></Suspense></div>}
  </PageCard>
}
