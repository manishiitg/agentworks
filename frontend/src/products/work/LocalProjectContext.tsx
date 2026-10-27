import { useEffect, useState } from 'react'
import { ChevronDown, FileText, FolderOpen, History, Loader2, RefreshCw, Sparkles } from 'lucide-react'
import { agentApi } from '../../services/api'

export type LocalProjectContextData = {
  folder: string
  instructions: { path: string; name: string }[]
  skills: { path: string; name: string }[]
  partial: boolean
  sessions_available: boolean
  sessions: { id: string; provider: string; account: string; title: string; updated_at: string }[]
}

export function LocalProjectContext({ projectId }: { projectId: string }) {
  const [data, setData] = useState<LocalProjectContextData | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [sessionsShown, setSessionsShown] = useState(false)
  const [resuming, setResuming] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    agentApi.getLocalProjectContext(projectId, sessionsShown).then(result => {
      if (!cancelled) setData(result)
    }).catch(() => {
      if (!cancelled) setError('Couldn’t check this folder. Try refreshing.')
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [projectId, revision, sessionsShown])

  const resume = async (id: string) => {
    setResuming(id)
    setError('')
    try {
      const conversation = await agentApi.resumeLocalProjectSession(projectId, id)
      window.dispatchEvent(new CustomEvent('agentworks:local-chat-opened', { detail: { projectId, conversationKey: conversation.conversation_key } }))
    } catch (cause) {
      const response = cause as { response?: { data?: { message?: string; error?: string } } }
      setError(response.response?.data?.message || response.response?.data?.error || 'Couldn’t open this conversation. Refresh and try again.')
    } finally { setResuming('') }
  }

  return <details className="group shrink-0 border-b border-border/60 text-xs" aria-label="Project context">
    <summary className="flex min-h-9 cursor-pointer list-none items-center gap-2 px-3 py-1.5 text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
      <FolderOpen className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate">{data?.folder || 'Project folder'}</span>
      <span className="ml-auto shrink-0 tabular-nums">{data ? `${data.instructions.length} instruction${data.instructions.length === 1 ? '' : 's'} · ${data.skills.length} skill${data.skills.length === 1 ? '' : 's'}` : loading ? 'Checking…' : 'Check folder'}</span>
      <ChevronDown className="h-3 w-3 shrink-0 transition-transform group-open:rotate-180" aria-hidden="true" />
    </summary>
    <div className="max-h-[40dvh] space-y-3 overflow-auto border-t border-border/40 px-3 py-3">
      <div className="flex items-center gap-2 text-muted-foreground">
        <span className="flex-1">Found in this folder. Your agent decides which to load.</span>
        <button type="button" title="Refresh project context" aria-label="Refresh project context" onClick={() => setRevision(n => n + 1)} disabled={loading} className="rounded p-1.5 hover:bg-muted disabled:opacity-50"><RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} /></button>
      </div>
      {data && <div className="grid gap-3 sm:grid-cols-2">
        <div><h3 className="mb-1 flex items-center gap-1.5 font-medium"><FileText className="h-3 w-3" />Instructions</h3>
          {data.instructions.length ? <ul className="space-y-1 text-muted-foreground">{data.instructions.map(file => <li key={file.path} className="break-all" title={file.path}>{file.path}</li>)}</ul> : <p className="text-muted-foreground">None found</p>}
        </div>
        <div><h3 className="mb-1 flex items-center gap-1.5 font-medium"><Sparkles className="h-3 w-3" />Skills</h3>
          {data.skills.length ? <ul className="space-y-1 text-muted-foreground">{data.skills.map(skill => <li key={skill.path} title={skill.path}><span className="font-medium text-foreground">{skill.name}</span><span className="block break-all text-[11px]">{skill.path}</span></li>)}</ul> : <p className="text-muted-foreground">None found</p>}
        </div>
      </div>}
      {data?.partial && <p className="text-muted-foreground">Showing a partial scan. Large generated folders and external links are skipped.</p>}
      {data?.sessions_available && <div className="border-t border-border/50 pt-2">
        {!sessionsShown ? <button type="button" onClick={() => setSessionsShown(true)} className="inline-flex items-center gap-1.5 text-primary hover:underline"><History className="h-3.5 w-3.5" />Past Claude / Codex chats</button> : <>
          <h3 className="mb-1 font-medium">Past chats in this folder</h3>
          {loading ? <p className="text-muted-foreground">Checking conversations…</p> : data.sessions.length ? <ul className="divide-y divide-border/40">{data.sessions.map(session => <li key={session.id} className="flex items-center gap-2 py-2">
            <div className="min-w-0 flex-1"><p className="truncate" title={session.title}>{session.title}</p><p className="text-[11px] text-muted-foreground">{session.provider === 'claude-code' ? 'Claude' : 'Codex'} · {session.account} · {new Date(session.updated_at).toLocaleDateString()}</p></div>
            <button type="button" disabled={Boolean(resuming)} onClick={() => void resume(session.id)} className="inline-flex shrink-0 items-center gap-1 rounded px-2 py-1.5 text-primary hover:bg-muted disabled:opacity-50">{resuming === session.id && <Loader2 className="h-3 w-3 animate-spin" />}Open tab</button>
          </li>)}</ul> : <p className="text-muted-foreground">No saved conversations found for this folder in your available accounts.</p>}
        </>}
      </div>}
      {error && <p role="alert" className="text-destructive">{error}</p>}
    </div>
  </details>
}
