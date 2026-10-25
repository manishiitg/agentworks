import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { Button } from '../ui/Button'
import { useVaultSetupDraft } from './useVaultSetupDraft'

type Props = { workspacePath: string; tabId?: string; kind: 'apps' | 'secrets' }
interface SharedAccountGroup {
 group: { ID: string; Name: string; ProjectRoot?: string }; role: 'owner' | 'member'
 connections?: unknown[]; secret_names: string[]
}

/** Sharing existing accounts/secrets, never a second project-use inventory. */
export function ProjectAccountSharingPanel(props: Props) {
 const actor = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 return <AccountSharingContents key={`${server}:${actor}:${props.workspacePath}:${props.kind}`} {...props} />
}
function AccountSharingContents({ tabId, kind }: Props) {
 const setup = useVaultSetupDraft(tabId)
 const streaming = useChatStore(state => Boolean(tabId && state.chatTabs[tabId]?.isStreaming))
 const wasStreaming = useRef(false)
 const generation = useRef(0)
 const [groups, setGroups] = useState<SharedAccountGroup[]>([])
 const [error, setError] = useState('')
 const refresh = useCallback(async () => {
  const version = ++generation.current
  try { const data = JSON.parse(await agentApi.myVaultsOp({operation:'list'})); if(version===generation.current){setGroups(data.vaults || []);setError('')} }
  catch { if(version===generation.current)setError('Shared accounts could not be loaded. Refresh or ask in chat.') }
 }, [])
 useEffect(()=>{void refresh();const focus=()=>void refresh();window.addEventListener('focus',focus);return()=>{generation.current++;window.removeEventListener('focus',focus)}},[refresh])
 useEffect(()=>{if(wasStreaming.current&&!streaming)void refresh();wasStreaming.current=streaming},[streaming,refresh])
 const label=kind==='apps'?'Share accounts':'Share secrets'
 return <details className="rounded-lg border border-border p-3" aria-label={label}>
  <summary className="cursor-pointer font-medium">{label}</summary>
  <div className="space-y-3 pt-3">
   <p className="text-xs text-muted-foreground">Use existing sharing to let other people use an account or secret. Saving an account does not select it for this project.</p>
   {(error||setup.error)&&<p role="alert" className="text-xs text-destructive">{error||setup.error}</p>}
   <div className="flex gap-2"><Button size="sm" variant="outline" onClick={()=>void setup.open(`Help me share ${kind==='apps'?'an account':'a secret'} with people. Inspect saved accounts and sharing first, use the existing sharing tools and preserve unrelated grants. Never repeat secret values.`)}>Manage sharing in chat</Button><Button size="sm" variant="ghost" aria-label="Refresh sharing" onClick={()=>void refresh()}><RefreshCw className="h-4 w-4"/></Button></div>
   {groups.filter(group=>group.role==='owner'||(kind==='apps'?group.connections?.length:group.secret_names.length)).map(group=><div key={group.group.ID} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-3 text-xs">
    <div><p className="font-medium">{group.group.Name}</p><p className="mt-1 text-muted-foreground">{group.role==='owner'?'Managed by you':'Shared with you'} · {kind==='apps'?`${group.connections?.length||0} accounts`:`${group.secret_names.length} secrets`}</p></div>
    {group.role==='owner'&&<Button size="xs" variant="outline" onClick={()=>void setup.open(`Manage existing account/secret sharing named “${group.group.Name}” (sharing ID ${group.group.ID}). Inspect it, then help me change its accounts, secrets or people, sync tools, or remove an empty group. Keep unrelated permissions and project selections.`)}>Manage in chat</Button>}
   </div>)}
  </div>
 </details>
}

/** Existing company OAuth setup stays administrator-only beside its provider. */
export function CompanySignInSetup({provider,tabId}:{provider:'Google'|'Slack';tabId?:string}) {
 const admin=useAuthStore(state=>state.user?.is_admin===true)
 const setup=useVaultSetupDraft(tabId)
 if(!admin)return null
 return <details className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">Company sign-in setup</summary><p className="mt-2 text-xs text-muted-foreground">Set up the company app people use to connect {provider}.</p>{setup.error&&<p role="alert" className="text-xs text-destructive">{setup.error}</p>}<Button size="sm" variant="outline" className="mt-3" onClick={()=>void setup.open(`Help me configure the company ${provider} OAuth sign-in app. Inspect the existing setup, ask for required details and store secrets securely. Never repeat secret values. Preserve existing accounts and grants.`)}>Configure in chat</Button></details>
}
