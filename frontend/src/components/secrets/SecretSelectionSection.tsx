import { useCallback, useEffect, useState } from 'react'
import { KeyRound, RefreshCw, Eye, Copy, MessageCircle } from 'lucide-react'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { Input } from '../ui/Input'
import { SecretField } from '../ui/SecretField'
import { secretsApi } from '../../api/secrets'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { PROJECT_SECRETS_REFRESH_EVENT } from '../../utils/secretMutationRefresh'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
import SecretSourceView from './SecretSourceView'

interface SecretSelectionSectionProps {
 selectedSecrets:string[];onSecretChange:(names:string[])=>void|Promise<unknown>
 selectedGlobalSecrets?:string[]|null;onGlobalSecretChange?:(names:string[]|null)=>void|Promise<unknown>
 workflowPath?:string;fillAvailableHeight?:boolean;workspaceNoun?:string;workspaceSecretHeading?:string;showGlobalSecrets?:boolean
 projectSource?:'project'|'vault';workspaceSecretsAlwaysEnabled?:boolean;allowGlobalPromotion?:boolean;persistExplicitGlobalSelection?:boolean
 mode?:'project'|'vault'|'group';groupId?:string;groupSelectedNames?:string[];onGroupAccessChange?:(name:string,allowed:boolean)=>Promise<unknown>
}
type Row={name:string;managed?:boolean;encrypted_value?:string}
const displayName=(name:string)=>name.replace(/^VLT_[^_]+__/, '')
export function SecretSelectionSection({selectedSecrets,onSecretChange,selectedGlobalSecrets=[],onGlobalSecretChange,workflowPath='',workspaceSecretHeading,projectSource='project',mode='project',groupSelectedNames=[],onGroupAccessChange,workspaceSecretsAlwaysEnabled=false}:SecretSelectionSectionProps){
 const [rows,setRows]=useState<Row[]>([]);const [loading,setLoading]=useState(true);const [error,setError]=useState('');const [revealed,setRevealed]=useState<Record<string,string>>({})
 const [editing,setEditing]=useState<string|null>(null)
 const [secretName,setSecretName]=useState('')
 const [secretValue,setSecretValue]=useState('')
 const [saving,setSaving]=useState(false)
 const canWrite=useCanWriteWorkflow(workflowPath||undefined)
 const shared=mode!=='project'||projectSource==='vault'
 const canManage=mode==='vault'||mode==='project'&&!shared&&canWrite
 const load=useCallback(async()=>{setLoading(true);try{setRows(shared?await secretsApi.getGlobalSecrets(mode==='vault'):await secretsApi.listWorkflowSecrets(workflowPath));setError('')}catch(cause){setError(cause instanceof Error?cause.message:'Secrets are unavailable.')}finally{setLoading(false)}},[shared,mode,workflowPath])
 useEffect(()=>{void load();window.addEventListener(PROJECT_SECRETS_REFRESH_EVENT,load);return()=>window.removeEventListener(PROJECT_SECRETS_REFRESH_EVENT,load)},[load])
 const ask=async(message:string)=>{try{
  const store=useChatStore.getState();const active=store.activeTabId?store.chatTabs[store.activeTabId]:undefined
  if(active?.metadata?.agentProfileWorkspace===workflowPath || !workflowPath&&active?.metadata?.agentProfileId==='caplayer') await openWorkspacePaneChatWithDraft({tabId:active!.tabId,message})
  else await openWorkspacePaneChatWithDraft({workspacePath:workflowPath||'Chats/CapLayer',message})
 }catch(cause){setError(cause instanceof Error?cause.message:'Could not open chat.')}}
 const selected=mode==='group'?groupSelectedNames:shared?(selectedGlobalSecrets??[]):selectedSecrets
 const toggle=async(name:string)=>{try{const on=!selected.includes(name);if(mode==='group'){await onGroupAccessChange?.(name,on);return}const next=on?[...selected,name]:selected.filter(item=>item!==name);if(shared)await onGlobalSecretChange?.(next);else await onSecretChange(next)}catch(cause){setError(cause instanceof Error?cause.message:'Could not save selection.')}}
 const reveal=async(row:Row)=>{try{const value=shared?await secretsApi.revealGlobalSecret(row.name):await secretsApi.decrypt(row.encrypted_value!,workflowPath);setRevealed(old=>({...old,[row.name]:value.value}))}catch(cause){setError(cause instanceof Error?cause.message:'Could not reveal this secret.')}}
 const edit=(name='')=>{setEditing(name);setSecretName(name);setSecretValue('');setError('')}
 const closeEditor=()=>{setEditing(null);setSecretValue('');setSecretName('')}
 const saveSecret=async()=>{
  if(!canManage||shared)return
  setSaving(true);setError('')
  try{
   const name=secretName.trim()
   const {encrypted}=await secretsApi.encrypt(secretValue)
   await secretsApi.storeWorkflowSecret(workflowPath,name,encrypted)
   closeEditor()
   setRevealed(old=>{const next={...old};delete next[name];return next})
   await load()
   if(!selectedSecrets.includes(name)){
    try{await onSecretChange([...selectedSecrets,name])}
    catch{setError('The secret was saved. Tick its name to use it in this project.')}
   }
  }catch(cause){setError(cause instanceof Error?cause.message:'Could not save this secret.')}
  finally{setSaving(false)}
 }
 const removeSecret=async(name:string)=>{
  if(!canManage||shared)return
  setSaving(true);setError('')
  try{await secretsApi.deleteWorkflowSecret(workflowPath,name);await onSecretChange(selectedSecrets.filter(item=>item!==name));setRevealed(old=>{const next={...old};delete next[name];return next});await load()}
  catch(cause){setError(cause instanceof Error?cause.message:'Could not remove this secret.')}
  finally{setSaving(false)}
 }
 return <section className="space-y-3" aria-label={workspaceSecretHeading||'Secrets'}>
  <div className="flex items-center justify-between gap-2"><h3 className="text-sm font-medium">{workspaceSecretHeading||'Secrets'}</h3><Button size="sm" variant="ghost" aria-label="Refresh secrets" onClick={()=>void load()}><RefreshCw className="h-3.5 w-3.5"/></Button></div>
  <p className="text-xs text-muted-foreground">Choose the secrets this project uses. Add or replace a project secret here, or manage sharing in chat. Saved values stay hidden.</p>
  {error&&<p role="alert" className="text-xs text-destructive">{error}</p>}
  {loading?<p className="text-xs text-muted-foreground">Loading Secrets…</p>:<div className="divide-y divide-border rounded-md border border-border">
   {rows.map(row=><div key={row.name} className="space-y-2 px-3 py-2.5"><div className="flex flex-wrap items-center gap-2">
    {mode!=='vault'&&<Checkbox aria-label={`Use ${displayName(row.name)}`} checked={workspaceSecretsAlwaysEnabled||selected.includes(row.name)} disabled={workspaceSecretsAlwaysEnabled||mode==='project'&&!canWrite} onCheckedChange={()=>void toggle(row.name)}/>}
    <KeyRound className="h-3.5 w-3.5 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1 break-all font-mono text-xs" title={row.name}>{displayName(row.name)}</span>
    {canManage&&<><Button size="xs" variant="ghost" aria-label={`Reveal ${displayName(row.name)}`} onClick={()=>void reveal(row)}><Eye className="h-3.5 w-3.5"/></Button>{!shared&&!row.managed&&<><Button size="xs" variant="outline" disabled={saving} onClick={()=>edit(row.name)}>Replace</Button><Button size="xs" variant="ghost" disabled={saving} onClick={()=>void removeSecret(row.name)}>Remove secret</Button></>}<Button size="xs" variant="ghost" onClick={()=>void ask(`Manage the Vault secret ${row.name}${workflowPath?' for this project':''}. Help me rotate, remove or share it through the existing Vault tools. Never repeat its value; preserve existing permissions.`)}>Manage in chat</Button></>}
   </div>{revealed[row.name]!==undefined&&<div className="flex gap-2"><code className="min-w-0 flex-1 break-all text-xs">{revealed[row.name]}</code><Button size="xs" variant="ghost" aria-label={`Copy ${displayName(row.name)}`} onClick={()=>void navigator.clipboard.writeText(revealed[row.name])}><Copy className="h-3.5 w-3.5"/></Button><Button size="xs" variant="ghost" onClick={()=>setRevealed(old=>{const next={...old};delete next[row.name];return next})}>Hide</Button></div>}</div>)}
   {!rows.length&&<p className="p-3 text-xs text-muted-foreground">No Secrets available here yet.</p>}
  </div>}
  {canManage&&!shared&&<Button size="sm" variant="outline" onClick={()=>edit()}>Add a secret</Button>}
  {canManage&&<Button size="sm" variant="ghost" onClick={()=>void ask(`Add a secret in Vault${workflowPath?' for this project':''}. Ask for its name and value, store it with the existing secret tool, and select it here. Never repeat the value or change unrelated grants.`)}><MessageCircle className="mr-2 h-3.5 w-3.5"/>Add a secret in chat</Button>}
  {editing!==null&&<form aria-label={editing?`Replace ${editing}`:'Add project secret'} className="space-y-3 rounded-lg border border-border p-3" onSubmit={event=>{event.preventDefault();void saveSecret()}}>
   <label className="block text-xs">Secret name<Input aria-label="Secret name" value={secretName} disabled={Boolean(editing)||saving} onChange={event=>setSecretName(event.target.value)} /></label>
   <SecretField label="Secret value" value={secretValue} onChange={setSecretValue} disabled={saving} />
   <p className="text-xs text-muted-foreground">The value is encrypted before saving and is not loaded back into this form.</p>
   <div className="flex gap-2"><Button size="sm" type="submit" disabled={saving||!secretName.trim()||!secretValue}>{saving?'Saving…':'Save secret'}</Button><Button size="sm" variant="ghost" type="button" disabled={saving} onClick={closeEditor}>Cancel</Button></div>
  </form>}
  {workflowPath&&<details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Where these secrets are used</summary><div className="mt-2"><SecretSourceView workflowPath={workflowPath}/></div></details>}
 </section>
}
export default SecretSelectionSection
