import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, Download, FileText, Folder, Trash2 } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { teamError, teamRequest, type Team, type TeamTarget } from './teamApi'

type TeamFile = { path: string; size: number; hash: string; revision: number; author: string; modified_at: string }
type Node = { name: string; path: string; folders: Map<string, Node>; files: TeamFile[] }

const mb = (bytes: number) => bytes < 1024 ? `${bytes} B` : bytes < 1 << 20 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / (1 << 20)).toFixed(1)} MB`

function tree(files: TeamFile[]): Node {
  const root: Node = { name: '', path: '', folders: new Map(), files: [] }
  for (const file of files) {
    const parts = file.path.split('/')
    let node = root
    for (const part of parts.slice(0, -1)) {
      const path = node.path ? `${node.path}/${part}` : part
      if (!node.folders.has(part)) node.folders.set(part, { name: part, path, folders: new Map(), files: [] })
      node = node.folders.get(part)!
    }
    node.files.push(file)
  }
  return root
}

/** A Team member's Files in someone else's Crew (owner, 5 Nov): exactly what the Team shares, from the server's Team
 * folder (the same content the sync and their agents' copy use), read and write. Nothing else of the owner's Crew. */
export function TeamFilesPanel({ team, target }: { team: Team; target: TeamTarget }) {
  const [files, setFiles] = useState<TeamFile[]>()
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState<TeamFile>()
  const [text, setText] = useState<string>()
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    try { setFiles((await teamRequest<{ files: TeamFile[] }>(target, '/files')).files || []); setError('') } catch (e) { setError(teamError(e)) }
  }, [target])
  useEffect(() => { void load() }, [load])
  const root = useMemo(() => tree(files || []), [files])
  const pick = async (file: TeamFile) => {
    setSelected(file); setText(undefined); setError('')
    try {
      const body = await teamRequest<string>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, true)
      setText(body); setDraft(body)
    } catch (e) { setError(teamError(e)) }
  }
  const change = async (method: 'PUT' | 'DELETE', body?: string) => {
    if (!selected) return
    setBusy(true); setError('')
    const transfer = crypto.randomUUID()
    try {
      await teamRequest(target, `/file?path=${encodeURIComponent(selected.path)}&base=${selected.hash}&transfer=${transfer}`, method, body, method === 'PUT')
      if (method === 'DELETE') { setSelected(undefined); setText(undefined) }
      await load()
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const download = async () => {
    if (!selected) return
    try {
      const blob = await teamRequest<Blob>(target, `/file?path=${encodeURIComponent(selected.path)}`, 'GET', undefined, 'blob')
      const link = document.createElement('a')
      link.href = URL.createObjectURL(blob); link.download = selected.path.split('/').pop() || 'file'; link.click()
      URL.revokeObjectURL(link.href)
    } catch (e) { setError(teamError(e)) }
  }
  const toggle = (path: string) => setOpen(current => { const next = new Set(current); if (next.has(path)) next.delete(path); else next.add(path); return next })
  const render = (node: Node, depth: number): ReactNode => <>
    {[...node.folders.values()].sort((a, b) => a.name.localeCompare(b.name)).map(folder => <div key={folder.path}>
      <button type="button" onClick={() => toggle(folder.path)} style={{ paddingLeft: depth * 14 + 8 }} className="flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-sm hover:bg-muted">
        {open.has(folder.path) ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}<Folder className="h-3.5 w-3.5 text-amber-500" />{folder.name}
      </button>
      {open.has(folder.path) && render(folder, depth + 1)}
    </div>)}
    {node.files.sort((a, b) => a.path.localeCompare(b.path)).map(file => <button key={file.path} type="button" onClick={() => void pick(file)} style={{ paddingLeft: depth * 14 + 26 }}
      className={`flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-sm hover:bg-muted ${selected?.path === file.path ? 'bg-muted' : ''}`}>
      <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1 truncate">{file.path.split('/').pop()}</span><span className="text-[11px] text-muted-foreground">{mb(file.size)}</span>
    </button>)}
  </>
  const shared = team.folder.whole ? 'the whole Crew' : (team.folder.folders || []).map(f => `${f}/`).join(', ')
  return <div className="flex h-full min-h-0 flex-col gap-2 p-3">
    <p className="text-xs text-muted-foreground">Shared with the team by {team.owner_name || 'its owner'}: {shared || 'nothing yet'}. You and your agents read and edit these files; the rest of this Crew stays with its owner.</p>
    {error && <p role="alert" className="text-xs text-amber-600">{error}</p>}
    <div className="flex min-h-0 flex-1 gap-3">
      <div className="min-h-0 w-64 shrink-0 overflow-auto rounded-lg border border-border py-1">
        {!files ? <p className="p-3 text-xs text-muted-foreground">Loading…</p> : files.length ? render(root, 0) : <p className="p-3 text-xs text-muted-foreground">No shared files yet.</p>}
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-2">
        {selected ? <>
          <div className="flex items-center gap-2 text-sm"><span className="min-w-0 flex-1 truncate font-medium">{selected.path}</span><span className="text-xs text-muted-foreground">by {selected.author}</span>
            <Button size="sm" variant="outline" className="h-7" onClick={() => void download()}><Download className="h-3.5 w-3.5" /></Button>
            <Button size="sm" variant="outline" className="h-7" disabled={busy} aria-label={`Delete ${selected.path}`} onClick={() => void change('DELETE')}><Trash2 className="h-3.5 w-3.5" /></Button></div>
          {text === undefined ? <p className="text-xs text-muted-foreground">Opening…</p> : <>
            <textarea aria-label={`Edit ${selected.path}`} value={draft} onChange={event => setDraft(event.target.value)} className="min-h-0 flex-1 resize-none rounded-lg border border-border bg-background p-2 font-mono text-xs" />
            <div><Button size="sm" disabled={busy || draft === text} onClick={() => void change('PUT', draft)}>Save</Button></div>
          </>}
        </> : <p className="text-xs text-muted-foreground">Choose a file to open it.</p>}
      </div>
    </div>
  </div>
}
