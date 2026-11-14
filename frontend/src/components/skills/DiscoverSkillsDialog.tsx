import { useEffect, useRef, useState } from 'react'
import { skillsApi, type DiscoveredSkill, type NativeSkillDiscovery } from '../../api/skills'
import ModalPortal from '../ui/ModalPortal'
import SkillImportDialog from './SkillImportDialog'

/** One project-scoped picker shared by Skills and Add agent. Nothing is
 * attached to a chat or imported until the person explicitly chooses Import. */
export default function DiscoverSkillsDialog({ workspacePath, close, onImported }: {
  workspacePath: string; close: () => void; onImported?: () => void
}) {
  const [provider, setProvider] = useState('claude-code')
  const [inventory, setInventory] = useState<NativeSkillDiscovery>({ skills: [], message: '' })
  const [search, setSearch] = useState('')
  const [preview, setPreview] = useState<DiscoveredSkill>()
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [upload, setUpload] = useState(false)
  const generation = useRef(0)
  const dialog = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    dialog.current?.focus()
    return () => previous?.focus()
  }, [])
  useEffect(() => {
    const version = ++generation.current
    setLoading(true); setError(''); setPreview(undefined); setInventory({ skills: [], message: '' })
    skillsApi.discover(workspacePath, provider).then(data => {
      if (version === generation.current) setInventory(data as NativeSkillDiscovery)
    }).catch(() => { if (version === generation.current) setError('Discovery is unavailable. Try again or import from GitHub or a ZIP.') })
      .finally(() => { if (version === generation.current) setLoading(false) })
    return () => { generation.current++ }
  }, [workspacePath, provider])
  const read = async (row: DiscoveredSkill) => {
    const version = ++generation.current
    setLoading(true); setError(''); setPreview(undefined)
    try { const skill = await skillsApi.discover(workspacePath, provider, row.id); if (version === generation.current) setPreview(skill as DiscoveredSkill) }
    catch { if (version === generation.current) setError('This skill is no longer available. Reopen discovery to refresh.') }
    finally { if (version === generation.current) setLoading(false) }
  }
  const importPreview = async () => {
    if (!preview) return
    const version = generation.current
    setBusy(true); setError('')
    try {
      const result = await skillsApi.importDiscovered(workspacePath, provider, preview.id)
      if (version !== generation.current) return
      if (!result.success) throw new Error(result.error || 'Import failed')
      setNotice(`Imported ${result.skill_name} into this project. The original is unchanged.`)
      setPreview(undefined); onImported?.()
    } catch (cause) { if (version === generation.current) setError(cause instanceof Error ? cause.message : 'Import failed') }
    finally { setBusy(false) }
  }
  if (upload) return <SkillImportDialog workspacePath={workspacePath} onClose={() => setUpload(false)} onSuccess={() => { setUpload(false); setNotice('Skill imported into this project.'); onImported?.() }} />
  return <ModalPortal><div className="fixed inset-0 z-[115] flex items-center justify-center bg-black/50 p-4">
    <section ref={dialog} tabIndex={-1} role="dialog" aria-modal="true" aria-label="Discover and import skills" className="flex max-h-[85vh] w-full max-w-2xl flex-col rounded-xl border border-border bg-background p-5 shadow-xl" onKeyDown={event => {
      if (event.key === 'Escape' && !busy) close()
      if (event.key === 'Tab') {
        const elements = [...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled)') ?? [])]
        const first = elements[0], last = elements.at(-1)
        if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { event.preventDefault(); last?.focus() }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
      }
    }}>
      <div className="flex items-center justify-between gap-2"><h2 className="text-base font-semibold">Discover &amp; import skills</h2><button type="button" disabled={busy} onClick={close} className="rounded px-2 py-1 text-sm hover:bg-muted">Close</button></div>
      <p className="mt-2 text-sm text-muted-foreground">Preview existing skills, then copy the ones you choose into this project. Discovery changes nothing; originals stay untouched.</p>
      <div className="my-4 flex flex-wrap gap-2"><select aria-label="Skill source program" value={provider} disabled={busy} onChange={event => setProvider(event.target.value)} className="rounded border border-input bg-background p-2 text-sm"><option value="claude-code">Claude</option><option value="codex-cli">Codex</option><option value="cursor-cli">Cursor</option></select><input autoFocus aria-label="Search discovered skills" placeholder="Search skills…" value={search} onChange={event => setSearch(event.target.value)} className="min-w-0 flex-1 rounded border border-input bg-background px-3 text-sm" /><button type="button" disabled={busy} onClick={() => setUpload(true)} className="rounded border border-border px-3 py-2 text-sm">GitHub / ZIP…</button></div>
      {error && <p role="alert" className="mb-2 text-sm text-destructive">{error}</p>}
      {notice && <p role="status" className="mb-2 text-sm">{notice}</p>}
      {loading ? <p role="status" className="text-sm text-muted-foreground">Reading skills…</p> : preview ? <div className="min-h-0 overflow-auto">
        <button type="button" disabled={busy} onClick={() => setPreview(undefined)} className="mb-3 text-sm underline">Back to discovered skills</button>
        <h3 className="font-medium">{preview.name}</h3><p className="text-xs text-muted-foreground">{preview.source}</p>
        <pre className="my-3 whitespace-pre-wrap break-words rounded bg-muted p-3 text-xs">{preview.document}</pre>
        <p className="mb-3 text-xs text-muted-foreground">Copies {preview.files?.length || 0} files, including references and scripts. No script is run.</p>
        <button type="button" disabled={busy} onClick={() => { void importPreview() }} className="rounded bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{busy ? 'Importing…' : 'Import into this project'}</button>
      </div> : <div className="min-h-0 overflow-auto"><p className="mb-3 text-xs text-muted-foreground">{inventory.message}</p>{inventory.skills.filter(row => `${row.name} ${row.description} ${row.source}`.toLowerCase().includes(search.toLowerCase())).map(row => <button type="button" key={row.id} onClick={() => { void read(row) }} className="block w-full border-b border-border px-2 py-3 text-left hover:bg-muted"><span className="text-sm font-medium">{row.name}</span><span className="ml-2 text-xs text-muted-foreground">{row.source}</span><span className="mt-1 block text-xs text-muted-foreground">{row.description}</span></button>)}{!inventory.skills.length && <p className="py-3 text-sm text-muted-foreground">No native skills found here. Import from GitHub or a ZIP instead.</p>}</div>}
    </section>
  </div></ModalPortal>
}
