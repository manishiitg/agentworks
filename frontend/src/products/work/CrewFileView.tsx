import { lazy, Suspense, useEffect, useState } from 'react'
import api from '../../services/api'
import { PageCard, RowButton } from '../../components/automation/AutomationKit'

const Code = lazy(() => import('../../components/ui/SyntaxHighlightedCode'))
const languageOf = (file: string) => file.endsWith('.py') ? 'python' : file.endsWith('.json') ? 'json' : file.endsWith('.md') ? 'markdown' : 'text'

/** A workflow's file, read only: people change a workflow by asking their agent in chat, never by hand. */
export function CrewFileView({ workspacePath, file, onClose, onAsk }: {
  workspacePath: string
  file: string
  onClose: () => void
  onAsk: (task: string) => void | Promise<void>
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
  const lines = content === null ? 0 : content.split('\n').length
  const isDark = document.documentElement.classList.contains('dark')
  return <PageCard title={file} hint={content === null ? undefined : `${lines} lines · your agent changes it, ask in chat`} onAsk={() => onAsk(`Help me change ${file}.`)}
    action={<RowButton onClick={onClose}>Back</RowButton>} testId="crew-file-view">
    {error ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{error}</p>
      : content === null ? <p className="px-4 py-3 text-sm text-muted-foreground">Loading…</p>
      : <div className="max-h-[75vh] overflow-auto"><Suspense fallback={<p className="px-4 py-3 text-sm text-muted-foreground">Loading…</p>}><Code code={content} language={languageOf(file)} isDark={isDark} showLineNumbers /></Suspense></div>}
  </PageCard>
}
