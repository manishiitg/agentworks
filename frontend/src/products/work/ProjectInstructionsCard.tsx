import { useEffect, useState } from 'react'
import { Loader2, ScrollText } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Textarea } from '../../components/ui/Textarea'
import { StatusBanner } from '../../components/workflow/bots/StatusBanner'
import api, { agentApi } from '../../services/api'
import { responseContent } from '../../utils/plannerFiles'

/** The project's own instructions file; the server appends it below the platform's instructions (PLAT-692). */
export const PROJECT_INSTRUCTIONS_FILE = 'PROJECT_INSTRUCTIONS.md'
const PROJECT_INSTRUCTIONS_MAX_BYTES = 32 * 1024

/** Code and Crew "Project instructions" editor (owners only), reading and writing PROJECT_INSTRUCTIONS.md at the project root. */
type InstructionFile = { name: string; bytes: number; method: 'injected' | 'native' | 'found' }
export function ProjectInstructionsCard({ workspacePath, projectId, provider, model }: { workspacePath: string; projectId?: string; provider?: string; model?: string }) {
  const path = `${workspacePath.replace(/\/+$/, '')}/${PROJECT_INSTRUCTIONS_FILE}`
  const [files, setFiles] = useState<InstructionFile[]>([])
  const [folderError, setFolderError] = useState('')
  const [saved, setSaved] = useState('')
  const [draft, setDraft] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    agentApi.getPlannerFileContent(path).then(response => {
      if (cancelled) return
      const content = responseContent(response)?.content ?? ''
      setSaved(content)
      setDraft(content)
    }).catch((cause: unknown) => {
      if (cancelled) return
      const status = (cause as { response?: { status?: number } })?.response?.status
      if (status === 404) {
        setSaved('')
        setDraft('')
      } else {
        setError(cause instanceof Error ? cause.message : 'Could not load the project instructions.')
      }
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [path])

  useEffect(() => {
    const abort = new AbortController()
    setFiles([]); setFolderError('')
    if (!projectId || !provider) return
    void api.get('/api/agent-profiles/work/local-project', { params: { project_id: projectId, instruction_status: true, provider, model }, signal: abort.signal }).then(({ data }) => {
      if (!abort.signal.aborted) setFiles(data.files || [])
    }).catch(cause => {
      if (!abort.signal.aborted && cause?.response?.status !== 404) setFolderError('Folder instruction details are unavailable.')
    })
    return () => abort.abort()
  }, [projectId, provider, model, saved])

  const bytes = new TextEncoder().encode(draft).length
  const dirty = draft !== saved
  const save = async () => {
    if (!dirty || saving) return
    setSaving(true)
    setError('')
    try {
      await agentApi.updatePlannerFile(path, draft, 'Update project instructions')
      setSaved(draft)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not save the project instructions.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingsCard
      icon={<ScrollText aria-hidden="true" className="h-4 w-4 text-primary" />}
      title="Project instructions"
      description="Added to the end of the agent's instructions on every message; the platform's own instructions stay above it."
    >
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {files.length > 0 && <div className="space-y-1 text-xs text-muted-foreground">
        <p className="font-medium text-foreground">Found in the project folder</p>
        {files.map(file => <p key={file.name}><span className="font-mono">{file.name}</span> · {file.method === 'injected' ? 'Will be added to the agent’s instructions' : file.method === 'native' ? 'Handled by the selected AI program; reading is not confirmed' : 'Found only; not added for this AI program'}{file.method === 'injected' && file.bytes > PROJECT_INSTRUCTIONS_MAX_BYTES ? ' · first 32 KB only' : ''}</p>)}
        <p>Based on the selected AI program for the next turn. Folder-specific rules keep their scope. Existing files are not changed.</p>
      </div>}
      {folderError && <p className="text-xs text-muted-foreground">{folderError}</p>}
      <Textarea
        aria-label="Project instructions"
        value={draft}
        onChange={event => setDraft(event.target.value)}
        disabled={loading || saving}
        placeholder={loading ? 'Loading…' : 'e.g. We always use pnpm. Run the linter before saying a change is done.'}
        rows={8}
      />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className={`text-[11px] ${bytes > PROJECT_INSTRUCTIONS_MAX_BYTES ? 'text-destructive' : 'text-muted-foreground'}`}>
          {PROJECT_INSTRUCTIONS_FILE} · {Math.ceil(bytes / 1024)} of 32 KB{bytes > PROJECT_INSTRUCTIONS_MAX_BYTES ? ' (only the first 32 KB is used)' : ''}
        </p>
        <Button onClick={() => void save()} disabled={!dirty || saving || loading}>
          {saving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving…</> : 'Save'}
        </Button>
      </div>
    </SettingsCard>
  )
}
