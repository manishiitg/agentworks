import { useCallback, useEffect, useState } from 'react'
import api from '../../services/api'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
interface Client { name: string; project_id?: string }
interface Setup { enabled: boolean; connection_id: string; infrastructure_ready: boolean; receiving: boolean; error?: string; mailboxes: { id: string; email: string; client_name: string }[]; provisioning: { oauth_clients: Client[]; job?: { stage: string; review_url?: string; error?: string } } }
export function CompanyAgentEmail() {
 const server = useCapabilitiesStore(state => state.capabilities?.local_mode === false)
 const [state, setState] = useState<Setup | null>(null)
 const [connection, setConnection] = useState('')
 const [client, setClient] = useState('')
 const [project, setProject] = useState('')
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState('')
 const [auth, setAuth] = useState('')
 const load = useCallback(async () => { if (!server) return; try {const data = (await api.get<Setup>('/api/agent-email/setup')).data; setState(data); setConnection(old => old || data.connection_id || data.mailboxes[0]?.id || ''); setClient(old => old || data.provisioning.oauth_clients[0]?.name || '')} catch {setError('Could not load company inbox setup. Try refreshing.')} }, [server])
 useEffect(() => {void load(); const refresh = () => {void load()}; window.addEventListener('focus', refresh); return () => window.removeEventListener('focus', refresh)}, [load])
 const act = async (action: 'connect' | 'prepare' | 'configure') => {
  setBusy(true); setError('')
  try {const data = (await api.post('/api/agent-email/setup', {action, ...(action === 'connect' ? {connection_id: connection || undefined, client_name: client || undefined} : action === 'prepare' ? {client_name: state?.mailboxes.find(item => item.id === connection)?.client_name || client || undefined, project_id: project || undefined} : {connection_id:connection, enabled:!state?.enabled})})).data; if (data.auth_url) setAuth(data.auth_url); await load(); if (action === 'configure') await useCapabilitiesStore.getState().fetchCapabilities()} catch (cause) {const data = (cause as {response?: {data?: unknown}})?.response?.data; setError(typeof data === 'string' ? data : 'Could not update company receiving. Please try again.')} finally {setBusy(false)}
 }
 if (!server) return null
 return <details aria-label="Company agent inboxes" className="rounded-lg border border-border p-4"><summary className="cursor-pointer text-sm font-semibold">Company agent inboxes (admin){state?.enabled ? ' · Enabled' : ''}</summary><div className="mt-3 space-y-3 text-xs">
  <p className="text-muted-foreground">Enable once to give every Crew, Code project, Goal and Relay its own inbox automatically. Mail appears in Inbox. Optional Watchers choose what work to start.</p>
  <p className="text-muted-foreground">These are separate AgentWorks inboxes backed by one company Gmail mailbox. Personal Google connections remain independent. Upload your company Google OAuth JSON under Sign-in apps above, then connect its receiving mailbox here once.</p>
  {error && <p role="alert" className="text-destructive">{error}</p>}
  {!!state?.provisioning.oauth_clients.length && <label className="block space-y-1"><span>Company Google app</span><select aria-label="Company inbox Google app" className="w-full rounded border border-border bg-background p-2" value={client} onChange={event => {setClient(event.target.value); setProject('')}}>{state.provisioning.oauth_clients.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</select></label>}
  {!!state?.mailboxes.length && <label className="block space-y-1"><span>Company receiving mailbox</span><select aria-label="Company receiving mailbox" className="w-full rounded border border-border bg-background p-2" value={connection} onChange={event => setConnection(event.target.value)}>{state.mailboxes.map(item => <option key={item.id} value={item.id}>{item.email || 'Finish company mailbox sign-in'}</option>)}</select></label>}
  <Button size="sm" variant="outline" disabled={busy || !client} onClick={() => void act('connect')}>{connection ? 'Reconnect company mailbox' : 'Connect company mailbox'}</Button>
  {auth && <p><a href={auth} target="_blank" rel="noreferrer" className="text-primary underline">Continue with Google for the company mailbox</a></p>}
  {state && !state.infrastructure_ready && <div className="space-y-2"><p>Set up Google’s mailbox delivery once for this server. You need permission to configure the Google Cloud project.</p>{!state.provisioning.oauth_clients.find(item => item.name === client)?.project_id && <label className="block space-y-1"><span>Google Cloud project ID</span><Input aria-label="Google Cloud project ID" value={project} onChange={event => setProject(event.target.value)} placeholder="Project that owns the Google app" /></label>}<Button size="sm" variant="outline" disabled={busy || !client} onClick={() => void act('prepare')}>Set up receiving</Button></div>}
  {state?.provisioning.job && <div className="space-y-1 rounded border border-border p-2"><p>{state.provisioning.job.stage}</p>{state.provisioning.job.error && <p className="text-destructive">{state.provisioning.job.error}</p>}{state.provisioning.job.review_url && <a className="text-primary underline" href={state.provisioning.job.review_url} target="_blank" rel="noreferrer">Review Google receiving setup</a>}</div>}
  {state?.infrastructure_ready && <p>{state.receiving && state.enabled ? 'Company mailbox connected. Send a real email to an agent’s address to test delivery.' : 'Google receiving infrastructure is set up.'}{state.error && <span className="text-destructive"> {state.error}</span>}</p>}
  <div className="flex flex-wrap gap-2"><Button size="sm" disabled={busy || !state || !state.enabled && (!connection || !state.infrastructure_ready)} onClick={() => void act('configure')}>{state?.enabled ? 'Disable agent inboxes' : 'Enable all agent inboxes'}</Button><Button size="sm" variant="ghost" disabled={busy} onClick={() => void load()}>Refresh setup</Button></div>
 </div></details>
}
