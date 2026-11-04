import { useEffect, useRef, useState } from 'react'
import { Download, FileText, Folder, RefreshCw, Upload } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { desktopRequest } from '../../services/desktopConnections'
import { TeamSyncPanel } from './TeamSyncPanel'
import { teamError, teamRequest, type Team, type TeamTarget } from './teamApi'

type Selection = { team: Team; target: TeamTarget }
type SharedFile = { path: string; size: number; hash: string; author: string; revision: number }

/** A team folder's Files and Sync history, opened from inside the shared Crew by a member who edits it (owner, 5 Nov:
 * no separate Teams page). */
export function TeamFolderPanel({ team, target, onAsk }: Selection & { onAsk: () => void }) {
  const [files, setFiles] = useState<SharedFile[]>([])
  const [selected, setSelected] = useState<SharedFile>()
  const [text, setText] = useState('')
  const [saved, setSaved] = useState('')
  const [version, setVersion] = useState(0)
  const [sync, setSync] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [newPath, setNewPath] = useState('')
  const upload = useRef<HTMLInputElement>(null)
  const [previousFolders, setPreviousFolders] = useState<string[]>([])
  useEffect(() => {
    if (!target.connection) return
    let active = true
    void desktopRequest<{ previous_folders?: string[] }>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.project)}/local`).then(data => { if (active) setPreviousFolders(data.previous_folders || []) }).catch(() => {})
    return () => { active = false }
  }, [target.connection, target.project, target.folderId])
  useEffect(() => {
    let active = true
    const load = () => teamRequest<{ files: SharedFile[] }>(target, '/files').then(data => { if (active) setFiles(data.files) }).catch(e => { if (active) setError(teamError(e)) })
    void load(); const timer = window.setInterval(() => { if (!document.hidden) void load() }, 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [target.project, target.connection, version])
  const open = async (file: SharedFile) => {
    if (text !== saved && selected?.path !== file.path) { setError('Save your current edit before opening another file.'); return }
    if (file.size > 1 << 20) { setError('This file is too large for the text editor. Use Download to open it on your computer.'); return }
    setBusy(true); setError('')
    try { const blob = await teamRequest<Blob>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, 'blob'); const content = new TextDecoder('utf-8', { fatal: true }).decode(await blob.arrayBuffer()); if (content.includes('\0')) throw new Error('This is a binary file. Use Download to open it on your computer.'); setSelected(file); setText(content); setSaved(content) } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const download = async (file: SharedFile) => {
    setBusy(true); setError('')
    try {
      const blob = await teamRequest<Blob>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, 'blob')
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a'); link.href = url; link.download = file.path.split('/').pop() || 'file'; link.click()
      window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const put = async (path: string, content: string | Blob, base = '') => {
    setBusy(true); setError('')
    try {
      await teamRequest(target, `/file?${new URLSearchParams({ path, base, transfer: crypto.randomUUID() })}`, 'PUT', content, true)
      setVersion(v => v + 1)
      if (typeof content === 'string') { setSaved(content); const data = await teamRequest<{ files: SharedFile[] }>(target, '/files'); setFiles(data.files); setSelected(data.files.find(f => f.path === path)) }
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  return <div className="flex min-h-0 flex-1 flex-col">
    <nav aria-label="Team folder views" className="flex items-center gap-2 border-b border-border px-4 py-2"><Button size="sm" variant={sync ? 'ghost' : 'secondary'} onClick={() => setSync(false)}><Folder className="mr-1.5 h-4 w-4" />Files</Button><Button size="sm" variant={sync ? 'secondary' : 'ghost'} onClick={() => setSync(true)}><RefreshCw className="mr-1.5 h-4 w-4" />Sync</Button><span className="ml-auto truncate text-xs text-muted-foreground">{team.folder.path}/</span></nav>
    {sync ? <div className="min-h-0 flex-1 overflow-auto p-4"><div className="mx-auto max-w-2xl"><TeamSyncPanel target={target} name={team.name} onAsk={onAsk} /></div></div> : <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <aside aria-label="Shared files" className="max-h-64 shrink-0 overflow-auto border-b border-border p-3 md:max-h-none md:w-64 md:border-b-0 md:border-r">
        <div className="mb-3 flex gap-2"><Button size="sm" variant="outline" disabled={busy} onClick={() => upload.current?.click()}><Upload className="mr-1 h-3 w-3" />Upload</Button><input ref={upload} type="file" className="hidden" aria-label="Upload shared file" onChange={e => { const f = e.target.files?.[0]; if (f) void put(f.name, f, files.find(p => p.path === f.name)?.hash); e.target.value = '' }} /></div>
        <form className="mb-3 flex gap-1" onSubmit={e => { e.preventDefault(); if (busy || !newPath.trim()) return; if (text !== saved) { setError('Save your current edit before creating another file.'); return }; if (files.some(f => f.path === newPath.trim())) { setError('That file already exists. Open it to edit.'); return }; setSelected(undefined); setText(''); setSaved(''); void put(newPath.trim(), ''); setNewPath('') }}><input aria-label="New file path" placeholder="New file…" value={newPath} onChange={e => setNewPath(e.target.value)} className="min-w-0 flex-1 rounded border border-border bg-transparent px-2 py-1 text-xs" /><Button size="sm" variant="outline" disabled={busy || !newPath.trim()}>Add</Button></form>
        {files.map(file => <div key={file.path} className="flex items-center"><button type="button" onClick={() => void open(file)} disabled={busy} aria-current={selected?.path === file.path ? 'page' : undefined} className={`flex w-full items-center gap-2 rounded px-2 py-2 text-left text-xs ${selected?.path === file.path ? 'bg-muted' : 'hover:bg-muted/60'}`}><FileText className="h-3.5 w-3.5 shrink-0" /><span className="break-all">{file.path}</span></button><button type="button" disabled={busy} aria-label={`Download ${file.path}`} onClick={() => void download(file)} className="rounded p-2 hover:bg-muted"><Download className="h-3.5 w-3.5" /></button></div>)}
        {!files.length && <p className="text-xs text-muted-foreground">Files appear here when the shared folder syncs.</p>}
      </aside>
      <section className="flex min-h-0 flex-1 flex-col p-4">{selected ? <><div className="mb-3 flex items-center gap-2"><h2 className="min-w-0 flex-1 truncate text-sm font-medium">{selected.path}</h2><span className="text-xs text-muted-foreground">{text !== saved ? 'Unsaved' : 'Saved'}</span><Button size="sm" disabled={busy || text === saved} onClick={() => void put(selected.path, text, selected.hash)}>Save</Button></div><textarea aria-label={`Edit ${selected.path}`} spellCheck={false} value={text} onChange={e => setText(e.target.value)} className="min-h-64 flex-1 resize-none rounded-lg border border-border bg-background p-3 font-mono text-xs focus:outline-none focus:ring-1 focus:ring-ring" /></> : <p className="text-sm text-muted-foreground">Choose a file to open it.</p>}</section>
    </div>}
    {!sync && previousFolders.map(path => <p key={path} className="border-t border-border px-4 py-2 text-xs text-muted-foreground">{path} (no longer shared)</p>)}
    {error && <p role="alert" className="border-t border-border px-4 py-2 text-xs text-destructive">{error}</p>}
  </div>
}
