import { useState } from 'react'
import { skillsApi } from '../../api/skills'

/** Browser folder upload, not an arbitrary host path or a new skill store. */
export default function SkillFolderImport({ workspacePath, back, onImported }: { workspacePath: string; back: () => void; onImported: () => void }) {
  const [files, setFiles] = useState<File[]>([])
  const [preview, setPreview] = useState<{ document?: string; name?: string; files?: string[] }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const submit = async (importing: boolean) => {
    setBusy(true); setError('')
    try {
      const result = await skillsApi.folder(workspacePath,files,importing)
      if (importing) { if (!result.success) throw new Error('Import failed'); onImported() }
      else setPreview(result)
    } catch (cause) {
      const data = (cause as { response?: { data?: unknown } })?.response?.data
      setError(typeof data === 'string' ? data : 'Could not read or import this folder. Check SKILL.md and try again.')
    } finally { setBusy(false) }
  }
  return <div className="min-h-0 space-y-3 overflow-auto">
    <button type="button" disabled={busy} onClick={back} className="text-sm underline">Back to discovery</button>
    <p className="text-sm text-muted-foreground">Choose one folder containing SKILL.md. Review its instructions before importing. Its original files stay untouched.</p>
    <input aria-label="Choose skill folder" type="file" multiple {...({ webkitdirectory: '', directory: '' })} disabled={busy} onChange={event => { setFiles(Array.from(event.target.files || [])); setPreview(undefined); setError('') }} className="block w-full text-sm" />
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {preview && <><h3 className="font-medium">{preview.name}</h3><pre className="whitespace-pre-wrap break-words rounded bg-muted p-3 text-xs">{preview.document}</pre><p className="text-xs text-muted-foreground">{preview.files?.length} files. Nothing runs during import.</p></>}
    <button type="button" disabled={busy || !files.length} onClick={() => { void submit(Boolean(preview)) }} className="rounded bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{busy ? 'Reading files…' : preview ? 'Import into this project' : 'Preview instructions'}</button>
  </div>
}
