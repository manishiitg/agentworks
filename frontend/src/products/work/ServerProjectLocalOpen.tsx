import { useEffect, useRef, useState } from 'react'
import { Button } from '../../components/ui/Button'
import { desktopRequest } from '../../services/desktopConnections'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

export type SourceProject = { dependencies?: { kind: string; name: string }[] }
type LocalProject = { project_id: string; folder: string; project: SourceProject; binding?: unknown }

export function ProjectConnectionNotes({ project }: { project?: SourceProject }) {
  const names = [...new Set(project?.dependencies?.map(item => item.name) || [])]
  if (!names.length) return null
  return <p className="text-muted-foreground">This project uses {names.join(', ')} on the server. Make these connections and secrets available on this Mac before using them locally. Server Vault values and logins are not copied.</p>
}

export function ServerProjectLocalOpen({ projectId, connection }: { projectId: string; connection: string }) {
  const [preview, setPreview] = useState<LocalProject | null>(null)
  const [folder, setFolder] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const path = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}/open-local`
  const currentPath = useRef(path)
  currentPath.current = path
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    setPreview(null); setFolder(''); setError(''); setBusy(false)
    void desktopRequest<LocalProject>(path).then(data => {
      if (!disposed && isChatIdentityCurrent(identity)) { setPreview(data); setFolder(data.folder) }
    }).catch(cause => {
      if (!disposed && isChatIdentityCurrent(identity)) setError(cause instanceof Error ? cause.message : 'Could not open this project on your Mac.')
    })
    return () => { disposed = true }
  }, [path])
  const open = async () => {
    const identity = captureChatIdentity()
    const current = () => isChatIdentityCurrent(identity) && currentPath.current === path
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<LocalProject>(path, 'POST', { folder })
      if (!current()) return
      void useConnectedWorkStore.getState().refresh()
      await window.electronAPI?.openDesktopConnection?.('local', `/crew/projects/${encodeURIComponent(data.project_id)}`)
    } catch (cause) {
      if (current()) setError(cause instanceof Error ? cause.message : 'Could not bring this project to your Mac. Retry from Project sync.')
    } finally { if (current()) setBusy(false) }
  }
  return <div className="space-y-3">
    <p>Open this project on your Mac with its files and main conversation. It keeps the same project identity and continues to sync with this server.</p>
    {preview && !preview.binding && <label className="block space-y-2"><span>Folder on this Mac</span><input aria-label="Folder on this Mac" value={folder} onChange={event => setFolder(event.target.value)} className="w-full rounded-md border border-border bg-background p-2" /><span className="block text-xs text-muted-foreground">Choose an empty folder. Existing files will stay where they are.</span></label>}
    <ProjectConnectionNotes project={preview?.project} />
    {error && <p role="alert" className="text-destructive">{error}</p>}
    <Button disabled={!preview || busy} onClick={() => void open()}>{busy ? 'Bringing project to this Mac…' : 'Open on this Mac'}</Button>
  </div>
}
