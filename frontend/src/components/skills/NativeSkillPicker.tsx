import { useEffect, useRef, useState } from 'react'
import { skillsApi, type NativeSkillCatalog, type NativeSkillChoice } from '../../api/skills'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import ModalPortal from '../ui/ModalPortal'

const EMPTY: NativeSkillCatalog = { skills: [], provider: '', prepared: false, supported: false, message: '' }

// Request-driven only. Connection/account/session changes discard stale
// results; the browser never decides provider support or resolves native paths.
export function useNativeSkillCatalog(workspace: string | undefined, session: string | undefined, provider: string | undefined, enabled: boolean) {
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([server,account,workspace,session,provider])
  const [loaded, setLoaded] = useState('')
  const [catalog, setCatalog] = useState(EMPTY)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    let cancelled = false
    if (!enabled || !workspace || !session) return
    setLoading(true); setError(''); setLoaded('')
    skillsApi.invocation(workspace,session,provider).then(value => {
      if (!cancelled) { setCatalog(value); setLoaded(scope) }
    }).catch(() => { if (!cancelled) { setCatalog(EMPTY); setLoaded(scope); setError('Skills are unavailable for this chat. Refresh or remove the selection before trying again.') } })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  },[scope,workspace,session,provider,enabled,revision])
  return { catalog: loaded === scope ? catalog : EMPTY, error: loaded === scope ? error : '', loading,
    refresh: () => setRevision(value => value + 1) }
}

export default function NativeSkillPicker({ catalog, loading, error, refresh, close, choose }: {
  catalog: NativeSkillCatalog; loading: boolean; error: string; refresh: () => void; close: () => void; choose: (skill: NativeSkillChoice) => void
}) {
  const [search,setSearch] = useState('')
  const dialog = useRef<HTMLElement>(null)
  useEffect(() => { const previous=document.activeElement as HTMLElement | null; return () => previous?.focus() },[])
  return <ModalPortal><div className="fixed inset-0 z-[115] flex items-center justify-center bg-black/50 p-4">
    <section ref={dialog} role="dialog" aria-modal="true" aria-label="Choose a skill for this message" className="flex max-h-[75vh] w-full max-w-lg flex-col rounded-xl border border-border bg-background p-4 shadow-xl" onKeyDown={event => {
      if (event.key === 'Escape') close()
      if (event.key === 'Tab') {
        const elements=[...(dialog.current?.querySelectorAll<HTMLElement>('input,button:not(:disabled)') ?? [])]
        if (event.shiftKey && document.activeElement===elements[0]) { event.preventDefault(); elements.at(-1)?.focus() }
        else if (!event.shiftKey && document.activeElement===elements.at(-1)) { event.preventDefault(); elements[0]?.focus() }
      }
    }}>
      <div className="flex items-center justify-between"><h2 className="font-semibold">Skills</h2><button type="button" onClick={close} className="rounded px-2 py-1 text-sm hover:bg-muted">Close</button></div>
      <p className="my-2 text-xs text-muted-foreground">One skill for this message. A request is not proof of execution. Nothing is imported here.</p>
      <input autoFocus aria-label="Search usable skills" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search skills…" className="my-2 rounded border border-input bg-background p-2 text-sm" />
      <div className="min-h-0 overflow-auto">
        {loading ? <p role="status" className="py-3 text-sm">Reading this chat's skills…</p> : <>
          <p role={error ? 'alert' : undefined} className="mb-2 text-xs text-muted-foreground">{error || catalog.message || 'Send a message in this chat to prepare its native skills, then refresh.'}</p>
          {catalog.skills.filter(skill => `${skill.name} ${skill.description}`.toLowerCase().includes(search.toLowerCase())).map(skill => <button type="button" key={skill.id} onClick={() => choose(skill)} className="block w-full border-b border-border px-2 py-3 text-left hover:bg-muted"><span className="text-sm font-medium">{skill.name}</span><span className="mt-1 block text-xs text-muted-foreground">{skill.description}</span><span className="mt-1 block text-xs text-muted-foreground">{skill.source}</span></button>)}
        </>}
      </div>
      <button type="button" onClick={refresh} disabled={loading} className="mt-3 self-start rounded border border-border px-3 py-1.5 text-sm disabled:opacity-50">Refresh skills</button>
    </section>
  </div></ModalPortal>
}
