import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, Folder, FolderOpen } from 'lucide-react'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeFolder } from '../../services/knowledgebaseApi'

function FolderBranch({ path, name, selected, onSelect, revision, root = false }: { path: string; name: string; selected: string; onSelect: (path: string) => void; revision: number; root?: boolean }) {
  const [open, setOpen] = useState(root)
  const [folders, setFolders] = useState<KnowledgeFolder[]>([])
  const [nextCursor, setNextCursor] = useState<string>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    setLoading(true); setError('')
    void knowledgebaseApi.folders(path, '', controller.signal).then(data => { setFolders(data.folders || []); setNextCursor(data.next_cursor) })
      .catch(error => { if (!controller.signal.aborted) { setFolders([]); setError(knowledgebaseError(error)) } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [path, open, revision])
  async function more() {
    if (!nextCursor || loading) return
    setLoading(true)
    try { const data = await knowledgebaseApi.folders(path, nextCursor); setFolders(previous => [...previous, ...(data.folders || [])]); setNextCursor(data.next_cursor) }
    catch (error) { setError(knowledgebaseError(error)) }
    finally { setLoading(false) }
  }
  return <li>
    <div className={`flex min-w-0 items-center rounded-md ${selected === path ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted'}`}>
      <button type="button" className="p-1.5" aria-label={`${open ? 'Collapse' : 'Expand'} ${name}`} aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}</button>
      <button type="button" className="flex min-w-0 flex-1 items-center gap-2 py-2 pr-2 text-left text-xs" onClick={() => { onSelect(path); setOpen(true) }} aria-current={selected === path ? 'location' : undefined}>{open ? <FolderOpen className="h-3.5 w-3.5 shrink-0" /> : <Folder className="h-3.5 w-3.5 shrink-0" />}<span className="truncate" title={path || name}>{name}</span></button>
    </div>
    {open && <ul className="ml-3 border-l border-border pl-1">{folders.filter(folder => folder.path !== path).map(folder => <FolderBranch key={folder.path} path={folder.path} name={folder.name || folder.path.split('/').at(-1) || folder.path} selected={selected} onSelect={onSelect} revision={revision} />)}
      {nextCursor && <li><button type="button" disabled={loading} onClick={() => void more()} className="px-3 py-2 text-xs text-primary">More folders…</button></li>}
      {loading && !folders.length && <li className="px-3 py-2 text-xs text-muted-foreground">Loading…</li>}
      {error && <li className="px-3 py-2 text-xs text-destructive" role="alert">{error}</li>}
    </ul>}
  </li>
}

export function KnowledgebaseFolderTree({ selected, onSelect, revision }: { selected: string; onSelect: (path: string) => void; revision: number }) {
  return <nav aria-label="Knowledge folders" className="min-w-0 overflow-auto p-2"><ul><FolderBranch path="" name="Organization" selected={selected} onSelect={onSelect} revision={revision} root /></ul></nav>
}
