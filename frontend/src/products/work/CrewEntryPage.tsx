import { useRef, useState, type FormEvent } from 'react'
import { ArrowRight, Loader2, Plus, RefreshCw, Search, UsersRound } from 'lucide-react'
import { EntityIdentityIcon } from '../../components/ui/EntityIdentityIcon'
import { WorkspaceTopToolbar } from '../../components/workspace/WorkspaceTopToolbar'
import { useChatStore } from '../../stores/useChatStore'
import { formatDeploymentDateTime } from '../../utils/displayTime'
import { workProjectActivity } from './workProjectPresentation'
import type { WorkSession } from './workSessions'
import { CREW_STARTER_TYPES, type CrewStarterType } from './crewStarterTypes'

/** Creation reuses the existing wizard. Rows are only the admitted Crew inventory. */
export function CrewEntryPage({ projects, loading, error, canCreate, onCreate, onOpen, onRefresh }: {
  projects: WorkSession[]
  loading: boolean
  error: string | null
  canCreate: boolean
  onCreate: (prompt: string, type?: CrewStarterType) => void
  onOpen: (project: WorkSession) => void
  onRefresh: () => Promise<unknown>
}) {
  const [prompt, setPrompt] = useState('')
  const [type, setType] = useState<CrewStarterType>()
  const [query, setQuery] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const [refreshError, setRefreshError] = useState<string | null>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  const active = useChatStore(state => state.activeSessionsCache)
  const filter = query.trim().toLocaleLowerCase()
  const visible = projects.filter(project => [project.identity?.name, project.title, project.description].some(value => value?.toLocaleLowerCase().includes(filter)))
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (canCreate && !loading) onCreate(prompt.trim(), type)
  }
  const refresh = async () => {
    setRefreshing(true); setRefreshError(null)
    try { await onRefresh() } catch (cause) { setRefreshError(cause instanceof Error ? cause.message : 'Could not refresh your Crews.') }
    finally { setRefreshing(false) }
  }
  return <div className="flex h-full min-h-0 flex-col bg-background" data-testid="crew-entry-page">
    <WorkspaceTopToolbar><h1 className="text-sm font-semibold">Crew</h1></WorkspaceTopToolbar>
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
      <div className="mx-auto w-full max-w-5xl px-4 pb-8 sm:px-6">
        {canCreate && <section aria-labelledby="crew-entry-title" className={projects.length ? 'pb-6 pt-6 sm:pt-8' : 'pb-8 pt-10 sm:pt-14'}>
          <div className="mx-auto max-w-2xl text-center">
            <span className="mx-auto mb-3 grid h-11 w-11 place-items-center rounded-xl bg-muted text-muted-foreground"><UsersRound aria-hidden="true" className="h-5 w-5" /></span>
            <h2 id="crew-entry-title" className="text-2xl font-semibold tracking-tight sm:text-3xl">An AI teammate for your work</h2>
            <p className="mt-2 text-sm text-muted-foreground">Research, prepare reports or handle work you do again.</p>
          </div>
          <form onSubmit={submit} className="mx-auto mt-5 max-w-3xl rounded-2xl border border-border bg-muted/20 p-3 focus-within:border-primary/60 focus-within:ring-1 focus-within:ring-primary/20 sm:p-4">
            <label htmlFor="crew-entry-prompt" className="sr-only">What would you like your Crew to do?</label>
            <textarea ref={input} id="crew-entry-prompt" value={prompt} onChange={event => setPrompt(event.target.value)} maxLength={1000} rows={2} disabled={loading}
              placeholder="What would you like your Crew to do?" className="block w-full resize-none bg-transparent text-sm leading-6 outline-none placeholder:text-muted-foreground disabled:opacity-50" />
            <div className="mt-2 flex justify-end"><button type="submit" disabled={loading} data-tour="crew-create" className="inline-flex min-h-9 items-center gap-2 rounded-lg bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
              <Plus aria-hidden="true" className="h-4 w-4" />Create Crew
            </button></div>
          </form>
          <div className="mx-auto mt-4 max-w-3xl">
            <div className="mb-2 flex items-center justify-between gap-2"><h3 className="text-xs font-medium text-muted-foreground">Choose a use case</h3>{type && <button type="button" onClick={() => setType(undefined)} className="rounded px-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Use my own idea</button>}</div>
            <div className="flex gap-2 overflow-x-auto overscroll-x-contain pb-2" aria-label="Crew use cases">
              {CREW_STARTER_TYPES.map(item => <button type="button" key={item.id} disabled={loading} aria-pressed={type === item.id}
                onClick={() => { setType(item.id); setPrompt(item.purpose); input.current?.focus() }} className={`w-60 shrink-0 rounded-xl border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:opacity-50 ${type === item.id ? 'border-primary/60 bg-primary/5' : 'border-border'}`}>
                <span className="flex items-center gap-2 text-xs font-semibold"><item.icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />{item.label}</span>
                <span className="mt-1 block text-xs text-muted-foreground">{item.summary}</span>
                <span className="mt-1.5 block text-xs leading-5 text-muted-foreground">{item.example}</span>
              </button>)}
            </div>
          </div>
        </section>}
        <section aria-labelledby="crew-existing-title" className={canCreate ? 'border-t border-border pt-5' : 'pt-6'}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 id="crew-existing-title" className="text-base font-semibold">Your Crews <span className="ml-1 text-xs font-normal text-muted-foreground">{projects.length}</span></h2>
            <div className="flex min-w-0 flex-1 items-center justify-end gap-2 sm:flex-none">
              <label className="flex min-w-0 max-w-xs flex-1 items-center gap-2 rounded-lg border border-border px-2.5 sm:w-60"><Search aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><input aria-label="Search Crews" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search Crews" className="h-9 min-w-0 w-full bg-transparent text-sm outline-none" /></label>
              <button type="button" onClick={() => { void refresh() }} disabled={refreshing || loading} title="Refresh Crews" aria-label="Refresh Crews" className="grid h-9 w-9 shrink-0 place-items-center rounded-lg border border-border text-muted-foreground hover:bg-muted disabled:opacity-50"><RefreshCw aria-hidden="true" className={`h-4 w-4 ${refreshing || loading ? 'animate-spin' : ''}`} /></button>
            </div>
          </div>
          {!canCreate && <p className="mt-2 text-sm text-muted-foreground">Open a Crew shared with you. Ask an administrator for access to create your own.</p>}
          {(refreshError || error) && <p role="alert" className="mt-3 text-sm text-destructive">{refreshError || error}</p>}
          {loading ? <p role="status" className="flex items-center gap-2 py-8 text-sm text-muted-foreground"><Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" />Loading your Crews…</p> : visible.length ? <ul className="mt-4 divide-y divide-border overflow-hidden rounded-xl border border-border">
            {visible.map(project => {
              const name = project.identity?.name?.trim() || project.title
              const activity = workProjectActivity(active, [], project.id)
              return <li key={`${project.product}:${project.id}`}><button type="button" onClick={() => onOpen(project)} className="flex min-h-16 w-full items-center gap-3 px-3 py-3 text-left hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring sm:px-4">
                <EntityIdentityIcon icon={project.identity?.icon} label={name} />
                <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{name}</span><span className="mt-1 block truncate text-xs text-muted-foreground">{project.shared ? 'Shared with you · ' : ''}{project.description || 'Open this Crew'}<span className="sm:hidden"> · Updated {formatDeploymentDateTime(project.updatedAt)}</span></span></span>
                {activity && <span className={`inline-flex shrink-0 items-center gap-1.5 text-xs ${activity === 'waiting' ? 'text-amber-500' : 'text-primary'}`}>{activity === 'running' && <Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />}{activity === 'waiting' ? 'Needs you' : 'Working'}</span>}
                <span className="hidden shrink-0 text-xs text-muted-foreground sm:block" title={formatDeploymentDateTime(project.updatedAt)}>Updated {new Date(project.updatedAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })}</span>
                <ArrowRight aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              </button></li>
            })}
          </ul> : <p className="py-8 text-sm text-muted-foreground">{filter ? 'No Crews match your search.' : error ? 'Refresh to try again.' : canCreate ? 'Your Crews will appear here. Describe your first one above.' : 'No Crews have been shared with you yet.'}</p>}
        </section>
      </div>
    </div>
  </div>
}
