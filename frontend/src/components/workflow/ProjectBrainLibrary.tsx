import { useEffect, useRef, useState } from 'react'
import { ArrowLeft, FileText, FolderOpen } from 'lucide-react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeEntry, type KnowledgeFolder } from '../../services/knowledgebaseApi'
import { Button } from '../ui/Button'

/** The person's existing library inventory, not a claim about project usage. */
export function ProjectBrainLibrary() {
  const [folder, setFolder] = useState('')
  const [folders, setFolders] = useState<KnowledgeFolder[]>([])
  const [entries, setEntries] = useState<KnowledgeEntry[]>([])
  const [cursors, setCursors] = useState<{ folders?: string; entries?: string }>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const generation = useRef(0)
  useEffect(() => {
    const controller = new AbortController(), version = ++generation.current
    setLoading(true); setError(''); setFolders([]); setEntries([]); setCursors({})
    Promise.all([knowledgebaseApi.folders(folder, '', controller.signal), knowledgebaseApi.entries({ folder_path: folder, limit: 100 }, controller.signal)])
      .then(([f, e]) => { if (version === generation.current) { setFolders(f.folders.filter(item => item.path !== folder)); setEntries(e.entries.filter(item => item.folder_path === folder)); setCursors({ folders: f.next_cursor, entries: e.next_cursor }) } })
      .catch(cause => { if (!controller.signal.aborted) setError(knowledgebaseError(cause)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => { generation.current++; controller.abort() }
  }, [folder, revision])
  async function more() {
    const version = generation.current
    setLoading(true); setError('')
    try {
      const [f, e] = await Promise.all([cursors.folders ? knowledgebaseApi.folders(folder, cursors.folders) : undefined, cursors.entries ? knowledgebaseApi.entries({ folder_path: folder, cursor: cursors.entries, limit: 100 }) : undefined])
      if (version !== generation.current) return
      if (f) setFolders(items => [...items, ...f.folders.filter(item => item.path !== folder)])
      if (e) setEntries(items => [...items, ...e.entries.filter(item => item.folder_path === folder)])
      setCursors({ folders: f?.next_cursor, entries: e?.next_cursor })
    } catch (cause) { if (version === generation.current) setError(knowledgebaseError(cause)) }
    finally { if (version === generation.current) setLoading(false) }
  }
  return <section aria-label="Knowledge available to you" className="overflow-hidden rounded-lg border border-border">
    <div className="space-y-1 border-b border-border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">Available to you</h3>{!loading && !error && <span className="text-muted-foreground">{folders.length} folders · {entries.length} items{cursors.folders || cursors.entries ? ' shown' : ''}</span>}</div>
      <p className="text-muted-foreground">Your folders and items. Project agents can use them only when the project allows it and everyone seeing its output has access.</p>
      {folder && <button className="flex items-center gap-1 pt-1 text-primary" onClick={() => setFolder(folder.split('/').slice(0, -1).join('/'))}><ArrowLeft className="h-3 w-3"/> {folder}</button>}
    </div>
    {error && <div role="alert" className="p-3 text-destructive">{error} <Button size="sm" variant="ghost" onClick={() => setRevision(value => value + 1)}>Retry</Button></div>}
    <ul className="divide-y divide-border">
      {folders.map(item => <li key={item.path}><button onClick={() => setFolder(item.path)} className="flex w-full items-center gap-2 px-3 py-2.5 text-left hover:bg-muted/40"><FolderOpen className="h-4 w-4 shrink-0 text-primary"/><span className="min-w-0 flex-1 truncate">{item.name}</span><span className="text-muted-foreground">{item.breadcrumb_only ? 'Contains shared items' : item.effective_role === 'owner' ? 'Manage' : item.effective_role === 'editor' ? 'Can edit' : 'Can open'}</span></button></li>)}
      {entries.map(item => <li key={item.entry_id} className="flex items-center gap-2 px-3 py-2.5"><FileText className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1 truncate" title={item.path}>{item.title || item.filename}</span><span className="text-muted-foreground">{item.type}</span>{item.updated_at && <time dateTime={item.updated_at} className="text-muted-foreground">{new Date(item.updated_at).toLocaleDateString()}</time>}</li>)}
    </ul>
    {loading && <p role="status" className="p-3 text-muted-foreground">Loading knowledge…</p>}
    {!loading && !error && !folders.length && !entries.length && !cursors.folders && !cursors.entries && <p className="p-4 text-muted-foreground">No knowledge in this folder. Ask in chat to add notes, files or skills.</p>}
    {(cursors.folders || cursors.entries) && <Button size="sm" variant="ghost" disabled={loading} onClick={() => void more()}>Show more</Button>}
  </section>
}
