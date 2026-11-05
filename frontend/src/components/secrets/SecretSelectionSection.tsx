import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
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
import { WorkspacePanelActions } from '../workspace/WorkspacePanelHeaderSlots'
import SecretSourceView from './SecretSourceView'

interface SecretSelectionSectionProps {
 /** A project's Vault picker; the project's own list and its Add form sit around it. */
 inventory?: ReactNode
 selectedGlobalSecrets?:string[]|null;onGlobalSecretChange?:(names:string[]|null)=>void|Promise<unknown>
 workflowPath?:string;fillAvailableHeight?:boolean;workspaceNoun?:string;workspaceSecretHeading?:string;showGlobalSecrets?:boolean
 projectSource?:'project'|'vault';persistExplicitGlobalSelection?:boolean
 mode?:'project'|'vault'|'group';groupId?:string;groupSelectedNames?:string[];onGroupAccessChange?:(name:string,allowed:boolean)=>Promise<unknown>
}
type Row={name:string;managed?:boolean}
const displayName=(name:string)=>name.replace(/^VLT_[^_]+__/, '')
/** Vault secrets: the administrator's Vault list, a group's grants, or the secrets a project uses. A project's new
 * secret is saved in its owner's Vault (My accounts) and used by the project in one step. */
export function SecretSelectionSection({selectedGlobalSecrets=[],onGlobalSecretChange,workflowPath='',workspaceSecretHeading,projectSource='project',mode='project',groupSelectedNames=[],onGroupAccessChange,inventory}:SecretSelectionSectionProps){
 const [rows,setRows]=useState<Row[]>([]);const [loading,setLoading]=useState(true);const [error,setError]=useState('');const [revealed,setRevealed]=useState<Record<string,string>>({})
 const [editing,setEditing]=useState(false)
 const [secretName,setSecretName]=useState('')
 const [secretValue,setSecretValue]=useState('')
 const [saving,setSaving]=useState(false)
 const [copied,setCopied]=useState('')
 const canWrite=useCanWriteWorkflow(workflowPath||undefined)
 const listed=mode!=='project'||projectSource==='vault'
 const canAdd=mode==='project'&&projectSource==='project'&&canWrite
 const canReveal=mode==='vault'
 const load=useCallback(async()=>{if(!listed){setLoading(false);return}setLoading(true);try{setRows(await secretsApi.getGlobalSecrets(mode==='vault'));setError('')}catch(cause){setError(cause instanceof Error?cause.message:'Secrets are unavailable.')}finally{setLoading(false)}},[listed,mode])
 // Values belong to one view: forget them, and drop late replies, when the view changes.
 const scope=useRef('');const scopeKey=`${mode}:${projectSource}:${workflowPath}`;scope.current=scopeKey
 useEffect(()=>{setRevealed({});setCopied('')},[scopeKey])
 useEffect(()=>{void load();window.addEventListener(PROJECT_SECRETS_REFRESH_EVENT,load);return()=>window.removeEventListener(PROJECT_SECRETS_REFRESH_EVENT,load)},[load])
 const ask=async(message:string)=>{try{
  const store=useChatStore.getState();const active=store.activeTabId?store.chatTabs[store.activeTabId]:undefined
  if(active?.metadata?.agentProfileWorkspace===workflowPath || !workflowPath&&active?.metadata?.agentProfileId==='caplayer') await openWorkspacePaneChatWithDraft({tabId:active!.tabId,message})
  else await openWorkspacePaneChatWithDraft({workspacePath:workflowPath||'Chats/CapLayer',message})
 }catch(cause){setError(cause instanceof Error?cause.message:'Could not open chat.')}}
 const selected=mode==='group'?groupSelectedNames:(selectedGlobalSecrets??[])
 const toggle=async(name:string)=>{try{const on=!selected.includes(name);if(mode==='group'){await onGroupAccessChange?.(name,on);return}await onGlobalSecretChange?.(on?[...selected,name]:selected.filter(item=>item!==name))}catch(cause){setError(cause instanceof Error?cause.message:'Could not save selection.')}}
 const fetchValue=async(row:Row)=>{const at=scope.current;const value=await secretsApi.revealGlobalSecret(row.name);return scope.current===at?value.value:null}
 const reveal=async(row:Row)=>{try{const value=await fetchValue(row);if(value!==null)setRevealed(old=>({...old,[row.name]:value}))}catch(cause){setError(cause instanceof Error?cause.message:'Could not reveal this secret.')}}
 // Copy asks the server each time, so a value whose access was removed is never copied from memory, and it is never shown.
 const copy=async(row:Row)=>{setCopied('');try{const value=await fetchValue(row);if(value===null)return;await navigator.clipboard.writeText(value);setCopied(row.name)}catch{setRevealed(old=>{const next={...old};delete next[row.name];return next});setError('Could not copy this secret.')}}
 const closeEditor=()=>{setEditing(false);setSecretValue('');setSecretName('')}
 const saveSecret=async()=>{
  if(!canAdd)return
  setSaving(true);setError('')
  try{
   const {encrypted}=await secretsApi.encrypt(secretValue)
   // The server saves it in the owner's Vault and selects it for this project.
   await secretsApi.storeWorkflowSecret(workflowPath,secretName.trim(),encrypted)
   closeEditor()
   window.dispatchEvent(new Event(PROJECT_SECRETS_REFRESH_EVENT))
  }catch(cause){setError(cause instanceof Error?cause.message:'Could not save this secret.')}
  finally{setSaving(false)}
 }
 const actionsFor=(row:Row)=>canReveal&&<div className="flex flex-wrap items-center gap-1.5"><Button size="xs" variant="ghost" aria-label={`Reveal ${displayName(row.name)}`} onClick={()=>void reveal(row)}><Eye className="h-3.5 w-3.5"/></Button><Button size="xs" variant="ghost" aria-label={`${copied===row.name?'Copied':'Copy'} ${displayName(row.name)}`} onClick={()=>void copy(row)}><Copy className="h-3.5 w-3.5"/></Button><Button size="xs" variant="ghost" onClick={()=>void ask(`Manage the Vault secret ${row.name}. Help me rotate, remove or share it through the existing Vault tools. Never repeat its value; preserve existing permissions.`)}>Manage in chat</Button>{revealed[row.name]!==undefined&&<div className="flex gap-2"><code className="min-w-0 flex-1 break-all text-xs">{revealed[row.name]}</code><Button size="xs" variant="ghost" onClick={()=>setRevealed(old=>{const next={...old};delete next[row.name];return next})}>Hide</Button></div>}</div>
 return <section className="space-y-3" aria-label={workspaceSecretHeading||'Secrets'}>
  {!inventory&&<><div className="flex items-center justify-between gap-2"><h3 className="text-sm font-medium">{workspaceSecretHeading||'Secrets'}</h3><Button size="sm" variant="ghost" aria-label="Refresh secrets" onClick={()=>void load()}><RefreshCw className="h-3.5 w-3.5"/></Button></div>
  <p className="text-xs text-muted-foreground">Choose the Vault secrets this uses. Manage sharing in chat. Saved values stay hidden.</p></>}
  {error&&<p role="alert" className="text-xs text-destructive">{error}</p>}
  {inventory??(listed&&(loading?<p className="text-xs text-muted-foreground">Loading Secrets…</p>:<div className="divide-y divide-border rounded-md border border-border">
   {rows.map(row=><div key={row.name} className="space-y-2 px-3 py-2.5"><div className="flex flex-wrap items-center gap-2">
    {mode!=='vault'&&<Checkbox aria-label={`Use ${displayName(row.name)}`} checked={selected.includes(row.name)} disabled={mode==='project'&&!canWrite} onCheckedChange={()=>void toggle(row.name)}/>}
    <KeyRound className="h-3.5 w-3.5 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1 break-all font-mono text-xs" title={row.name}>{displayName(row.name)}</span>
    {actionsFor(row)}
   </div></div>)}
   {!rows.length&&<p className="p-3 text-xs text-muted-foreground">No secrets available here yet.</p>}
  </div>))}
  <WorkspacePanelActions local={!inventory}>{canAdd&&<Button size="sm" variant="outline" onClick={()=>{setEditing(true);setError('')}}>Add a secret</Button>}
  {(canAdd||canReveal)&&!inventory&&<Button size="sm" variant="ghost" onClick={()=>void ask(`Add a secret in Vault${workflowPath?' for this project':''}. Ask for its name and value, store it with the existing secret tool, and select it here. Never repeat the value or change unrelated grants.`)}><MessageCircle className="mr-2 h-3.5 w-3.5"/>Add a secret in chat</Button>}</WorkspacePanelActions>
  {editing&&<form aria-label="Add a secret" className="space-y-3 rounded-lg border border-border p-3" onSubmit={event=>{event.preventDefault();void saveSecret()}}>
   <label className="block text-xs">Secret name<Input aria-label="Secret name" value={secretName} disabled={saving} onChange={event=>setSecretName(event.target.value)} /></label>
   <SecretField label="Secret value" value={secretValue} onChange={setSecretValue} disabled={saving} />
   <p className="text-xs text-muted-foreground">Saved in your Vault and used by this project. The value is encrypted and is not loaded back into this form. Saving a name you already have replaces its value.</p>
   <div className="flex gap-2"><Button size="sm" type="submit" disabled={saving||!secretName.trim()||!secretValue}>{saving?'Saving…':'Save secret'}</Button><Button size="sm" variant="ghost" type="button" disabled={saving} onClick={closeEditor}>Cancel</Button></div>
  </form>}
  {workflowPath&&<details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Where these secrets are used</summary><div className="mt-2"><SecretSourceView workflowPath={workflowPath}/></div></details>}
 </section>
}
export default SecretSelectionSection
