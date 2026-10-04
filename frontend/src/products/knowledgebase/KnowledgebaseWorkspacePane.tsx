import { useEffect, useRef, useState } from 'react'
import { BookOpen, Cloud, Search, X } from 'lucide-react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeAccess, type KnowledgeBackup, type KnowledgeEntry, type KnowledgeEvent, type KnowledgeRead } from '../../services/knowledgebaseApi'
import { KnowledgebaseFolderTree } from './KnowledgebaseFolderTree'
import { KnowledgebaseReader, backupLabel } from './KnowledgebaseReader'
import { KnowledgebaseAccessPanel } from './KnowledgebaseAccessPanel'
import { KnowledgebaseActivityPanel } from './KnowledgebaseActivityPanel'
import { KnowledgebaseConnectPanel } from './KnowledgebaseConnectPanel'

export type KnowledgebaseView = 'library' | 'access' | 'activity' | 'connect'
const input = 'rounded-md border border-border bg-background px-2.5 py-2 text-xs'
export function KnowledgebaseWorkspacePane({ view, folder, onFolder, onAsk, revision, isAdmin }: { view: KnowledgebaseView; folder: string; onFolder: (path: string) => void; onAsk: () => void; revision: number; isAdmin: boolean }) {
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [type, setType] = useState('')
  const [tag, setTag] = useState('')
  const [debouncedTag, setDebouncedTag] = useState('')
  const [entries, setEntries] = useState<Array<{ entry: KnowledgeEntry; excerpt?: string }>>([])
  const [access, setAccess] = useState<KnowledgeAccess | null>(null)
  const [events, setEvents] = useState<KnowledgeEvent[]>([])
  const [backup, setBackup] = useState<KnowledgeBackup | null>(null)
  const [nextCursor, setNextCursor] = useState<string>()
  const [selectedId, setSelectedId] = useState<string>()
  const [read, setRead] = useState<KnowledgeRead | null>(null)
  const [loading, setLoading] = useState(true)
  const [reading, setReading] = useState(false)
  const [error, setError] = useState('')
  const [readError, setReadError] = useState('')
  const [showFolders, setShowFolders] = useState(false)
  const generation = useRef(0)
  const previousContext = useRef('')
  const currentRead = useRef(read)
  currentRead.current = read
  useEffect(() => { const timer = window.setTimeout(() => { setDebouncedQuery(query.trim()); setDebouncedTag(tag.trim()) }, 250); return () => window.clearTimeout(timer) }, [query, tag])
  useEffect(() => { setSelectedId(undefined); setRead(null); setReadError('') }, [folder, debouncedQuery, type, debouncedTag])
  useEffect(() => {
    const current = ++generation.current
    const controller = new AbortController()
    setLoading(true); setError(''); setNextCursor(undefined)
    const context = JSON.stringify([view, folder, debouncedQuery, type, debouncedTag])
    if (context !== previousContext.current) {
      // Clear old-folder state immediately. Polls retain form and scroll state.
      setEntries([]); setEvents([]); setAccess(null); setBackup(null)
      previousContext.current = context
    }
    async function load() {
      if (view === 'library') {
        const params = { folder_path: folder, type, tag: debouncedTag }
        const listing = debouncedQuery ? knowledgebaseApi.search({ ...params, query: debouncedQuery }, controller.signal).then(data => ({ entries: data.results || [], next_cursor: data.next_cursor }))
          : knowledgebaseApi.entries(params, controller.signal).then(data => ({ entries: (data.entries || []).map(entry => ({ entry })), next_cursor: data.next_cursor }))
        const [result, backupResult] = await Promise.all([listing, knowledgebaseApi.backup(folder, controller.signal)])
        if (generation.current !== current) return
        setEntries(result.entries); setNextCursor(result.next_cursor); setBackup(backupResult)
      } else if (view === 'activity') {
        const result = await knowledgebaseApi.activity(folder, '', controller.signal)
        if (generation.current !== current) return
        setEvents(result.events || []); setNextCursor(result.next_cursor)
      } else {
        const result = await knowledgebaseApi.access(folder, controller.signal)
        if (generation.current === current) setAccess(result)
      }
    }
    void load().catch(error => { if (!controller.signal.aborted && generation.current === current) { setError(knowledgebaseError(error)); setEntries([]); setEvents([]); setAccess(null); setBackup(null); setRead(null); setSelectedId(undefined) } })
      .finally(() => { if (!controller.signal.aborted && generation.current === current) setLoading(false) })
    return () => controller.abort()
  }, [view, folder, debouncedQuery, type, debouncedTag, revision])
  useEffect(() => {
    if (!selectedId || view !== 'library') return
    const controller = new AbortController()
    const alreadyOpen = currentRead.current?.entry.entry_id === selectedId
    setReading(!alreadyOpen); setReadError('')
    if (!alreadyOpen) setRead(null)
    void knowledgebaseApi.read(selectedId, controller.signal).then(result => { if (!controller.signal.aborted) setRead(result) })
      .catch(error => { if (!controller.signal.aborted) { setRead(null); setReadError(knowledgebaseError(error)) } })
      .finally(() => { if (!controller.signal.aborted) setReading(false) })
    return () => controller.abort()
  }, [selectedId, view, revision])
  async function more() {
    if (!nextCursor || loading) return
    const current = generation.current
    setLoading(true); setError('')
    try {
      if (view === 'activity') {
        const result = await knowledgebaseApi.activity(folder, nextCursor)
        if (current !== generation.current) return
        setEvents(previous => [...previous, ...(result.events || [])]); setNextCursor(result.next_cursor)
      } else {
        const params = { folder_path: folder, type, tag: debouncedTag, cursor: nextCursor }
        const result = debouncedQuery ? await knowledgebaseApi.search({ ...params, query: debouncedQuery }) : await knowledgebaseApi.entries(params).then(data => ({ results: (data.entries || []).map(entry => ({ entry })), next_cursor: data.next_cursor }))
        if (current !== generation.current) return
        setEntries(previous => [...previous, ...(result.results || [])]); setNextCursor(result.next_cursor)
      }
    } catch (error) { if (current === generation.current) setError(knowledgebaseError(error)) }
    finally { if (current === generation.current) setLoading(false) }
  }
  const backupStatuses = new Map(backup?.entries?.map(entry => [entry.entry_id, entry.status]))
  return <div className="flex h-full min-h-0 min-w-0 flex-col">
    <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-border px-3 py-2 text-xs"><button type="button" className="rounded border border-border px-2 py-1 md:hidden" onClick={() => setShowFolders(value => !value)} aria-expanded={showFolders}>Folders</button><button type="button" onClick={() => onFolder('')} className="shrink-0 text-muted-foreground hover:text-primary">Organization</button>{folder && <><span className="text-muted-foreground">/</span><span className="truncate" title={folder}>{folder}</span></>}{view === 'library' && backup && <span className="ml-auto flex items-center gap-1 text-muted-foreground"><Cloud className="h-3.5 w-3.5" />{backup.configured ? 'Git backup' : 'Backup not configured'}</span>}</div>
    <div className="flex min-h-0 flex-1">
      <aside className={`${showFolders ? 'block w-48 shrink-0' : 'hidden'} border-r border-border bg-muted/10 md:block md:w-48 md:shrink-0`}><KnowledgebaseFolderTree selected={folder} onSelect={path => { onFolder(path); setShowFolders(false) }} revision={revision} /></aside>
      <main className="min-h-0 min-w-0 flex-1 overflow-y-auto" aria-label="Knowledge Base workspace">
        {view === 'library' && !selectedId && <div className="p-4 sm:p-5">
          <div className="mb-5 flex flex-wrap gap-2"><label className="relative min-w-32 flex-1"><Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" /><input aria-label="Search knowledge" className={`${input} w-full pl-8`} value={query} onChange={event => setQuery(event.target.value)} placeholder="Search this folder and descendants…" /></label><select aria-label="Filter knowledge type" value={type} onChange={event => setType(event.target.value)} className={input}><option value="">All types</option>{['skill', 'fact', 'note', 'source'].map(value => <option key={value} value={value}>{value[0].toUpperCase() + value.slice(1)}s</option>)}</select><input aria-label="Filter knowledge tag" className={`${input} w-28`} value={tag} onChange={event => setTag(event.target.value)} placeholder="Tag" />{(query || type || tag) && <button type="button" aria-label="Clear filters" className="p-2 text-muted-foreground hover:text-primary" onClick={() => { setQuery(''); setType(''); setTag('') }}><X className="h-4 w-4" /></button>}</div>
          <div className="mb-4 flex items-start justify-between gap-3"><div><h1 className="text-xl font-semibold">{debouncedQuery ? 'Search results' : 'Knowledge library'}</h1><p className="mt-1 text-xs text-muted-foreground">Skills, facts, notes, and sources · updated through MCP</p></div><button type="button" onClick={onAsk} className="shrink-0 rounded-md border border-border px-2.5 py-2 text-xs hover:bg-muted">Folder access</button></div>
          {loading && !entries.length ? <p className="py-10 text-center text-sm text-muted-foreground">Loading knowledge…</p> : !error && !entries.length ? <div className="rounded-xl border border-dashed border-border p-8 text-center"><BookOpen className="mx-auto mb-3 h-7 w-7 text-muted-foreground" /><p className="text-sm font-medium">{debouncedQuery || type || tag ? 'No matching knowledge' : 'No readable entries in this folder'}</p><p className="mt-2 text-xs leading-5 text-muted-foreground">{debouncedQuery || type || tag ? 'Try another keyword or clear the filters.' : 'Connect an agent to create content, or ask an Owner for access.'}</p></div> : <ul className="divide-y divide-border rounded-xl border border-border">{entries.map(({ entry, excerpt }, index) => <li key={`${entry.entry_id}:${index}`}><button type="button" className="w-full p-4 text-left hover:bg-muted/40" onClick={() => setSelectedId(entry.entry_id)}><div className="flex items-start justify-between gap-3"><h2 className="min-w-0 break-words text-sm font-semibold">{entry.title || entry.filename}</h2><span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[10px] capitalize">{entry.type}</span></div><p className="mt-1 break-all text-xs text-muted-foreground">{entry.path}</p>{(excerpt || entry.description) && <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{excerpt || entry.description}</p>}<div className="mt-3 flex flex-wrap gap-2 text-[10px] text-muted-foreground">{(entry.tags || []).map(tag => <span key={tag} className="rounded-full border border-border px-2 py-0.5">{tag}</span>)}<span>{backupLabel(backup?.configured === false ? 'not_configured' : backupStatuses.get(entry.entry_id) || entry.backup_status)}</span></div></button></li>)}</ul>}
          {nextCursor && <button type="button" disabled={loading} onClick={() => void more()} className="mt-4 rounded-md border border-border px-4 py-2 text-xs hover:bg-muted disabled:opacity-50">{loading ? 'Loading…' : 'Load more entries'}</button>}
        </div>}
        {view === 'library' && selectedId && (reading ? <p className="p-6 text-sm text-muted-foreground">Loading entry…</p> : read ? <KnowledgebaseReader read={{ ...read, entry: { ...read.entry, backup_status: backup?.configured === false ? 'not_configured' : backupStatuses.get(read.entry.entry_id) || read.entry.backup_status } }} onBack={() => setSelectedId(undefined)} /> : <div className="p-6"><button type="button" onClick={() => setSelectedId(undefined)} className="mb-3 text-xs text-primary">Back to library</button><p role="alert" className="text-sm text-destructive">{readError || 'This entry is no longer available.'}</p></div>)}
        {loading && view === 'access' && !access && <p className="p-6 text-sm text-muted-foreground">Loading…</p>}
        {view === 'access' && access && <KnowledgebaseAccessPanel access={access} onAsk={onAsk} onFolder={onFolder} />}
        {view === 'activity' && <KnowledgebaseActivityPanel events={events} nextCursor={nextCursor} loading={loading} onMore={() => void more()} />}
        {view === 'connect' && <KnowledgebaseConnectPanel folder={folder} isAdmin={isAdmin} identities={access?.identities || []} onAsk={onAsk} />}
        {error && <p role="alert" className="m-5 rounded-lg border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
      </main>
    </div>
  </div>
}
