import { useCallback, useEffect, useState } from 'react'
import api from '../../services/api'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'
import { Button } from '../ui/Button'

export function CompanyAgentEmail() {
 const server = useCapabilitiesStore(state => state.capabilities?.local_mode === false)
 const [enabled,setEnabled]=useState<boolean | null>(null)
 const [busy,setBusy]=useState(false)
 const [error,setError]=useState('')
 const load=useCallback(async()=>{if(!server)return;try{setEnabled((await api.get('/api/agent-email/setup')).data.enabled);setError('')}catch{setError('Could not load Crew inbox settings.')}},[server])
 useEffect(()=>{void load()},[load])
 const toggle=async()=>{setBusy(true);setError('');try{const result=await api.post('/api/agent-email/setup',{action:'configure',enabled:!enabled});setEnabled(result.data.enabled);await useCapabilitiesStore.getState().fetchCapabilities()}catch{setError('Could not update Crew inbox settings. Please try again.')}finally{setBusy(false)}}
 if(!server)return null
 return <section aria-label="Crew inboxes" className="space-y-3 rounded-lg border border-border p-4 text-xs">
  <h3 className="text-sm font-semibold">Crew inboxes</h3>
  <p className="text-muted-foreground">Enable email inboxes for this company. People connect Gmail in My Vault and choose Use for Crew inboxes. All their Crews get addresses automatically.</p>
  <p className="text-muted-foreground">Mail appears in each Crew’s Inbox. Optional Watchers decide what starts work. No company mailbox or Google Cloud receiving setup is needed.</p>
  {error && <p role="alert" className="text-destructive">{error}</p>}
  <Button size="sm" disabled={busy || enabled===null} onClick={()=>void toggle()}>{enabled ? 'Turn off Crew inboxes' : 'Turn on Crew inboxes'}</Button>
  {enabled===null && <Button size="sm" variant="ghost" disabled={busy} onClick={()=>void load()}>Retry</Button>}
 </section>
}
