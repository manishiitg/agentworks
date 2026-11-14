import { lazy, Suspense, useEffect, useState } from 'react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { assertChatIdentityCurrent, captureChatIdentity } from '../../utils/chatIdentity'
import { presentProjectAskConversation, type ProjectChat } from '../../utils/projectAskConversation'
import { WorkingTipsContext } from './workingTipsContext'

const ChatArea = lazy(() => import('../ChatArea'))

export function promptLinkInput(search: string) {
  const params = new URLSearchParams(search)
  const prompt = params.get('prompt') || ''
  if (!prompt.trim() || [...prompt].length > 8000 || search.length > 8192) throw new Error('This prompt link is empty or too long. Ask for a shorter link, or open a chat from your workspace.')
  // Only these three fields are read. No URL flag may choose a model,
  // permissions, attachments, commands or automatic sending.
  return { prompt, crew: params.get('crew') || '', agent: params.get('agent') || '' }
}

/** Untrusted link text becomes an ordinary, reviewable composer draft only. */
export function PromptLinkPage() {
  const [bound, setBound] = useState<{ tabId: string; workspace: string; prompt: string; conflict: boolean }>()
  const [error, setError] = useState('')
  useEffect(() => {
    const identity = captureChatIdentity(), abort = new AbortController()
    let cancelled = false
    const open = async () => {
      const input = promptLinkInput(window.location.search)
      const { data } = await api.post<ProjectChat>('/api/prompt-links/open', { crew: input.crew, agent: input.agent }, { signal: abort.signal, skipSessionContext: true })
      assertChatIdentityCurrent(identity)
      const tabId = await presentProjectAskConversation(data, { activate: false })
      if (cancelled) return
      assertChatIdentityCurrent(identity)
      const store = useChatStore.getState(), draft = store.getTabConfig(tabId)
      const conflict = Boolean((draft?.inputText?.trim() && draft.inputText !== input.prompt) || draft?.fileContext?.length)
      // Clear any old auto-run intent before mounting the composer. A prompt
      // link never calls the queue or a send action, even on an existing tab.
      store.setTabConfig(tabId, { autoRun: false, ...(!conflict ? { inputText: input.prompt } : {}) })
      setBound({ tabId, workspace: data.workspace, prompt: input.prompt, conflict })
    }
    void open().catch(reason => {
      if (cancelled || abort.signal.aborted) return
      const detail = reason?.response?.data?.error
      setError(typeof detail?.message === 'string' ? detail.message : typeof detail === 'string' ? detail : reason instanceof Error ? reason.message : 'Could not open this prompt. Choose a chat from your workspace.')
    })
    return () => { cancelled = true; abort.abort() }
  }, [])
  const chooseDraft = (replace: boolean) => {
    if (!bound) return
    if (replace) useChatStore.getState().setTabConfig(bound.tabId, { inputText: bound.prompt, fileContext: [], autoRun: false })
    setBound({ ...bound, conflict: false })
  }
  if (error) return <main className="grid h-full place-items-center p-6"><div className="max-w-md space-y-4 text-center text-sm"><p role="alert">{error}</p><a href="/home" className="underline">Choose a chat from your workspace</a></div></main>
  if (!bound) return <p role="status" className="p-6 text-sm text-muted-foreground">Opening prompt…</p>
  return <main className="flex h-full min-h-0 flex-col">
    <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3 text-sm"><p>Review the draft, then press Send when you are ready.</p><a href="/home" className="text-xs underline">Your workspace</a></header>
    {bound.conflict ? <section className="mx-auto w-full max-w-2xl space-y-4 p-6 text-sm">
      <p>This chat already has a draft. Keep it, or replace it with the prompt from the link?</p>
      <pre className="whitespace-pre-wrap break-words rounded-lg border border-border p-4 font-sans">{bound.prompt}</pre>
      <div className="flex gap-3"><button type="button" onClick={() => chooseDraft(false)} className="rounded-full border border-border px-3 py-2">Keep my draft</button><button type="button" onClick={() => chooseDraft(true)} className="rounded-full bg-primary px-3 py-2 text-primary-foreground">Replace draft</button></div>
    </section> : <WorkingTipsContext.Provider value={false}><Suspense fallback={<p role="status">Opening chat…</p>}><ChatArea tabId={bound.tabId} compact hideHeader previousChatsWorkspacePath={bound.workspace} landingContent={<div />} /></Suspense></WorkingTipsContext.Provider>}
  </main>
}
