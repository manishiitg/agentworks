import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { productRoutePath } from '../../utils/productRoutes'
import { Button } from '../../components/ui/Button'
import { useChatStore } from '../../stores/useChatStore'
import { localFileError } from './codeLocalFiles'

type Receipt = { operation_id: string; state: string; workspace_id?: string; agent_id?: string; conversation_key?: string; error?: string; message?: string; tab_message?: string }
type Preview = { account: { provider: string; model: string; use: string }; share: { mode: string; people?: string[] }; continuity: string; message?: string }
type Target = { workspace_id: string; name: string; mode: string }

/** The server captures, queues and activates the publication. This control only
 * supplies the person's choice and displays its durable receipt. */
export function CrewPublish({ sessionId, disabled }: { sessionId: string; disabled?: boolean }) {
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const [open, setOpen] = useState(false)
  const [targets, setTargets] = useState<Target[]>([])
  const [destination, setDestination] = useState('')
  const [name, setName] = useState('')
  const [preview, setPreview] = useState<Preview>()
  const [receipt, setReceipt] = useState<Receipt>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const submission = useRef('')
  const sourceId = tab?.metadata?.agentProfileProjectId
  const key = tab?.metadata?.agentProfileConversationKey
  const args = { source_workspace_id: `code:${sourceId}`, conversation_key: key, workspace_id: destination, name }
  useEffect(() => {
    if (!open) return
    let active = true
    setName(tab?.metadata?.agentProfileProjectTitle || 'Published work')
    void api.post<{ workspaces: Target[] }>('/api/workspaces', { action: 'list' })
      .then(({ data }) => { if (active) setTargets(data.workspaces.filter(item => item.mode === 'builder' && item.workspace_id !== `code:${sourceId}`)) })
      .catch(cause => { if (active) setError(localFileError(cause)) })
    return () => { active = false }
  }, [open, sourceId, tab?.metadata?.agentProfileProjectTitle])
  useEffect(() => { setPreview(undefined); submission.current = '' }, [name, destination, sourceId, key])
  useEffect(() => {
    if (!receipt || !['capturing', 'uploading', 'preparing'].includes(receipt.state)) return
    let active = true
    const timer = window.setTimeout(() => {
      void api.post<Receipt>('/api/workspaces', { action: 'publish_status', operation_id: receipt.operation_id })
        .then(({ data }) => { if (active) { setReceipt(data); setError('') } })
        .catch(cause => { if (active) { setError(`${localFileError(cause)} Your receipt is saved; check again to see its progress.`); setBusy(false) } })
    }, 2000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [receipt])
  const publish = async () => {
    setBusy(true); setError('')
    try {
      if (!preview) {
        const { data } = await api.post<Preview>('/api/workspaces', { ...args, action: 'publish_preview' })
        setPreview(data)
      } else {
        submission.current ||= globalThis.crypto.randomUUID()
        const { data } = await api.post<Receipt>('/api/workspaces', { ...args, action: 'publish', submission_id: submission.current })
        setReceipt(data)
      }
    } catch (cause) { setError(localFileError(cause)) }
    finally { setBusy(false) }
  }
  if (!sourceId || !key) return null
  if (!open) return <Button size="sm" variant="outline" disabled={disabled} onClick={() => setOpen(true)}>Publish to Crew</Button>
  const working = receipt && ['capturing', 'uploading', 'preparing'].includes(receipt.state)
  return <section aria-label="Publish to Crew" className="space-y-3 rounded-md border border-border p-3">
    <p className="text-sm font-medium">Publish your files and this chat</p>
    <p className="text-xs text-muted-foreground">Crew keeps working while your computer is offline. Source changes through publishing again; working results stay online.</p>
    {!receipt && <>
      <label className="block text-sm">Destination<select className="mt-1 block w-full rounded border border-border bg-background p-2" value={destination} onChange={event => setDestination(event.target.value)} disabled={busy}>
        <option value="">New Crew · Only me</option>
        {targets.map(target => <option key={target.workspace_id} value={target.workspace_id}>Add agent to {target.name}</option>)}
      </select></label>
      <label className="block text-sm">{destination ? 'Agent name' : 'Crew name'}<input className="mt-1 block w-full rounded border border-border bg-background p-2" value={name} onChange={event => setName(event.target.value)} maxLength={120} disabled={busy} /></label>
      {preview && <div className="space-y-1 text-xs text-muted-foreground"><p>Runs on the company {preview.account.provider} account · {preview.account.model}.</p><p>{preview.account.use}</p><p>{preview.share.mode === 'private' ? 'Share: Only me' : preview.share.mode === 'everyone' ? 'Share: Everyone' : 'Share: Chosen people'}. {preview.message}</p><p>{preview.continuity}</p><p>Login files and .env stay on your computer. Imported chat is private to Builders.</p></div>}
      <Button size="sm" disabled={busy || disabled || !name.trim()} onClick={() => void publish()}>{busy ? 'Preparing…' : preview ? 'Publish now' : 'Review publication'}</Button>
    </>}
    {working && <p role="status" className="text-sm">{receipt.state === 'capturing' ? 'Saving files and this chat…' : 'Preparing your online Crew…'}</p>}
    {receipt?.state === 'live' && <p role="status" className="text-sm">{receipt.message} {receipt.tab_message}</p>}
    {receipt?.state === 'failed' && <p role="alert" className="text-sm text-destructive">{receipt.error} Your earlier version is still live.</p>}
    {receipt?.workspace_id && receipt.state === 'live' && <a className="text-sm underline" href={productRoutePath({ surface: receipt.workspace_id.startsWith('code:') ? 'code' : 'work', id: receipt.workspace_id.split(':')[1] })}>Open Crew</a>}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {receipt && error && <Button size="sm" variant="outline" onClick={() => setReceipt({ ...receipt })}>Check progress</Button>}
    <Button size="sm" variant="ghost" disabled={busy} onClick={() => setOpen(false)}>{working ? 'Close (publishing continues)' : 'Close'}</Button>
  </section>
}
