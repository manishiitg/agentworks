import { useEffect, useRef, useState } from 'react'
import { MonitorCog, Square } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../../components/ui/Button'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type ComputerUseStatus = {
  enabled: boolean; installed: boolean; active: boolean; version: string; platform: string
  permission_setup_note?: string
  permissions?: { data?: { accessibility?: { state?: string }; screen_recording?: { state?: string } } }
}
export const computerUseURL = (projectId: string) => `/api/agent-profiles/work/projects/${encodeURIComponent(projectId)}/computer-use`

export function ComputerUsePanel({ projectId }: { projectId: string }) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${server}:${actor}:${projectId}`
  const currentScope = useRef(scope); currentScope.current = scope
  const generation = useRef(0)
  const [snapshot, setSnapshot] = useState<{ scope: string; value: ComputerUseStatus }>()
  const status = snapshot?.scope === scope ? snapshot.value : undefined
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const identity = captureChatIdentity(), abort = new AbortController()
    generation.current++
    setSnapshot(undefined); setBusy(false); setError('')
    const load = () => {
      if (document.hidden) return
      const request = ++generation.current
      void api.get<ComputerUseStatus>(computerUseURL(projectId), { signal: abort.signal })
        .then(({ data }) => { if (!abort.signal.aborted && request === generation.current && isChatIdentityCurrent(identity) && currentScope.current === scope) setSnapshot({ scope, value: data }) })
        .catch(() => { if (!abort.signal.aborted && request === generation.current && isChatIdentityCurrent(identity) && currentScope.current === scope) setError('Could not check Computer use. Try again.') })
    }
    load()
    const timer = window.setInterval(load, 3000)
    window.addEventListener('focus', load)
    return () => { abort.abort(); window.clearInterval(timer); window.removeEventListener('focus', load) }
  }, [scope, projectId, revision])
  const run = async (action: 'install' | 'permissions' | 'enable' | 'disable' | 'stop') => {
    const identity = captureChatIdentity()
    const current = () => isChatIdentityCurrent(identity) && currentScope.current === scope
    generation.current++; setBusy(true); setError('')
    try {
      await api.post(computerUseURL(projectId), { action }, { timeout: 180000 })
      if (!current()) return
      generation.current++
      const { data } = await api.get<ComputerUseStatus>(computerUseURL(projectId))
      if (current()) { setSnapshot({ scope, value: data }); window.dispatchEvent(new CustomEvent('computer-use-changed')) }
    } catch (cause) {
      if (current()) {
        const message = (cause as { response?: { data?: { error?: { message?: string } | string } } }).response?.data?.error
        setError(typeof message === 'string' ? message : message?.message || 'Could not update Computer use. Try again.')
      }
    } finally { if (current()) setBusy(false) }
  }
  const accessibility = status?.permissions?.data?.accessibility?.state
  const ready = accessibility === 'granted' || status?.platform !== 'darwin' && status?.installed
  return <section aria-label="Computer use" className="max-w-xl space-y-4 text-sm">
    <p className="text-muted-foreground">Give this Crew control of this computer, including apps, Terminal and clipboard.</p>
    {error && <p role="alert" className="text-destructive">{error} <button className="underline" onClick={() => setRevision(value => value + 1)}>Retry</button></p>}
    {!status && !error && <p role="status">Checking Computer use…</p>}
    {status && <div className="space-y-4 rounded-lg border border-border p-4">
      <div className="flex items-center gap-3"><MonitorCog aria-hidden="true" className="h-5 w-5 text-muted-foreground"/><div className="min-w-0 flex-1"><p className="font-medium">This computer</p><p role="status" className="text-xs text-muted-foreground">{status.active ? 'Desktop action running' : !status.installed ? 'Needs setup' : !ready ? 'Needs permission' : status.enabled ? 'Ready' : 'Off for this Crew'}</p></div>
        {status.installed && <button type="button" role="switch" aria-label="Enable computer use" aria-checked={status.enabled} disabled={busy} onClick={() => void run(status.enabled ? 'disable' : 'enable')} className={`relative h-6 w-10 shrink-0 rounded-full transition-colors disabled:opacity-50 ${status.enabled ? 'bg-primary' : 'bg-muted'}`}><span className={`absolute top-1 h-4 w-4 rounded-full bg-foreground transition-transform ${status.enabled ? 'left-5' : 'left-1'}`}/></button>}
      </div>
      {status.permission_setup_note && <p className="text-xs text-muted-foreground">{status.permission_setup_note}</p>}
      <div className="flex flex-wrap items-center gap-2">
        {!status.installed && <Button type="button" disabled={busy} onClick={() => void run('install')}>{busy ? 'Installing…' : 'Install computer use'}</Button>}
        {status.installed && status.platform === 'darwin' && <Button type="button" variant="outline" disabled={busy} onClick={() => void run('permissions')}>Open permission settings</Button>}
        {status.enabled && <Button type="button" variant="outline" onClick={() => void run('stop')}><Square aria-hidden="true" className="mr-1.5 h-3 w-3"/>Stop</Button>}
      </div>
    </div>}
    <p className="text-xs text-muted-foreground">Your phone or website chat can use this computer while this Crew runs here. Shared Crew users and server agents cannot use it.</p>
    {status?.platform === 'darwin' && <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">macOS permissions</summary><p className="mt-2">Approve the app named by the permission guide. macOS may give that app and processes it starts access to your screen. Accessibility controls apps; Screen Recording allows screenshots.</p></details>}
    <p className="text-xs text-muted-foreground">App text and screenshots used in chat go to your selected AI provider. Screenshots stay in private chat storage. Stop cancels pending commands; an action already sent may finish.</p>
  </section>
}
