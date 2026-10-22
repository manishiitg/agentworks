import { useCallback,useEffect,useState } from 'react'
import { Vault,RefreshCw,MessageCircle,KeyRound,Plug,Users } from 'lucide-react'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { WorkspacePageContent } from './workspace/WorkspacePageContent'
import { Button } from './ui/Button'
import { agentApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import PersonalAccountsPanel from './integrations/PersonalAccountsPanel'
import { VaultSlackBots } from './integrations/VaultSlackBots'
import SlackAdminPanel from './admin/SlackAdminPanel'
import { useVaultSetupDraft } from './integrations/useVaultSetupDraft'
interface VaultView { group:{ID:string;Name:string;Description?:string;ProjectRoot?:string};role:'owner'|'member';members:string[];connections?:{id:string;label:string;provider:string;status:string;sign_in_error?:string}[];secret_names:string[] }
export default function MyVaultsPage({embedded=false}:{embedded?:boolean}={}){
 const isAdmin=useAuthStore(state=>state.user?.is_admin===true)
 const setup=useVaultSetupDraft()
 const [vaults,setVaults]=useState<VaultView[]>([]);const [loading,setLoading]=useState(true);const [error,setError]=useState('')
 const refresh=useCallback(async()=>{setLoading(true);try{const result=JSON.parse(await agentApi.myVaultsOp({operation:'list'})) as {vaults?:VaultView[]};setVaults(result.vaults??[]);setError('')}catch(cause){setError(cause instanceof Error?cause.message:'Vault is unavailable.')}finally{setLoading(false)}},[])
 useEffect(()=>{void refresh();const focus=()=>void refresh();window.addEventListener('focus',focus);return()=>window.removeEventListener('focus',focus)},[refresh])
 return <section aria-label="My vaults" className="flex h-full min-h-0 flex-col bg-background">
  {!embedded&&<WorkspacePageHeader page="vaults" title="My vaults" icon={Vault}/>}
  <div className="min-h-0 flex-1 overflow-y-auto"><WorkspacePageContent width="form" className="space-y-5 text-sm">
   <div className="flex items-start justify-between gap-3"><div><h2 className="text-lg font-semibold">Accounts and secrets</h2><p className="mt-1 text-sm text-muted-foreground">Everything is stored in Vault. Set up accounts and secrets through chat, then choose where to use them.</p></div><Button size="sm" variant="ghost" aria-label="Refresh vaults" onClick={()=>void refresh()}><RefreshCw className="h-4 w-4"/></Button></div>
   {(error||setup.error)&&<p role="alert" className="text-sm text-destructive">{error||setup.error}</p>}
   <div className="flex flex-wrap gap-2"><Button size="sm" onClick={()=>void setup.open('Help me connect an app in my Vault through manage_my_vaults. Ask what I need, handle API-key or OAuth setup in chat, return the real provider link and verify discovery. Do not change project selections unless I ask.')}><Plug className="mr-2 h-4 w-4"/>Connect an app in chat</Button><Button size="sm" variant="outline" onClick={()=>void setup.open('Help me add a secret to a Vault I own, using manage_my_vaults set_secret. Ask for its name and value, store it encrypted and never repeat its value. Do not change project selections.')}><KeyRound className="mr-2 h-4 w-4"/>Add a secret in chat</Button><Button size="sm" variant="outline" onClick={()=>void setup.open('Help me create or share a Vault using manage_my_vaults. Preserve existing memberships and grants unless I ask to change them.')}><Users className="mr-2 h-4 w-4"/>Create or share a vault</Button></div>
   {loading&&<p className="text-muted-foreground">Loading your vaults…</p>}
   {!loading&&!vaults.length&&<p className="rounded-lg border border-border p-4 text-muted-foreground">No vaults yet. Chat can help you connect your first account.</p>}
   {vaults.map(vault=><section key={vault.group.ID} className="overflow-hidden rounded-lg border border-border"><div className="flex items-start justify-between gap-3 bg-muted/20 p-4"><div><h3 className="font-semibold">{vault.group.Name}</h3><p className="mt-1 text-xs text-muted-foreground">{vault.group.ProjectRoot?'Used by this project under its existing sharing rules':`${vault.members.length} people · ${vault.role==='owner'?'You manage this vault':'Shared with you'}`}</p></div>{vault.role==='owner'&&<Button size="sm" variant="outline" onClick={()=>void setup.open(`Manage Vault ${vault.group.ID} (${vault.group.Name}) through manage_my_vaults. Help me add or remove accounts, rotate or remove secrets, manage members/owners, sync tools or delete an empty vault. Inspect current state and keep unrelated permissions. This request is for this Vault; do not change the active project's selections.`)}><MessageCircle className="mr-2 h-3.5 w-3.5"/>Manage in chat</Button>}</div>
    <div className="divide-y divide-border">{vault.connections?.map(connection=><div key={connection.id} className="flex items-center justify-between gap-3 px-4 py-3"><div><p className="text-sm font-medium">{connection.label||connection.provider}</p><p className="text-xs text-muted-foreground">{connection.sign_in_error|| (connection.status==='active'?'Connected':connection.status==='authentication_required'?'Sign-in required':'Not connected')}</p></div>{vault.role==='owner'&&<Button size="sm" variant="ghost" onClick={()=>void setup.open(`Finish setup or manage connection ${connection.id} in Vault ${vault.group.ID}. Use manage_my_vaults sign_in on this exact connection, guide missing app/key details here, return the real provider URL and verify discovery. Do not create another account or change project selections.`)}>Open in chat</Button>}</div>)}
     {vault.secret_names.map(name=><p key={name} className="flex items-center gap-2 break-all px-4 py-3 font-mono text-xs"><KeyRound className="h-3.5 w-3.5 shrink-0 text-muted-foreground"/>{name.split('__').slice(1).join('__')||name}</p>)}
     {!vault.connections?.length&&!vault.secret_names.length&&<p className="px-4 py-3 text-xs text-muted-foreground">No accounts or secrets yet.</p>}
    </div></section>)}
   <PersonalAccountsPanel/><VaultSlackBots/>
   {isAdmin&&<details className="rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium">Company sign-in apps</summary><p className="mt-2 text-xs text-muted-foreground">Register or update a provider's shared OAuth app through Vault chat.</p><Button size="sm" variant="outline" className="mt-3" onClick={()=>void setup.open('Configure a company OAuth sign-in app through manage_vault_access configure_sign_in_app. Inspect the provider app keys, ask for the required client ID and secret, store them encrypted and never repeat secret values. Preserve existing accounts and grants.')}>Configure in chat</Button></details>}
   {isAdmin&&<details className="rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium">Company Slack bot</summary><div className="mt-3"><SlackAdminPanel/></div></details>}
  </WorkspacePageContent></div>
 </section>
}
