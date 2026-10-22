import { useState } from 'react'
import { McpConnectionsPanel } from './McpConnectionsPanel'
import { useVaultMcpConnections } from './useVaultMcpConnections'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'

/** Vault is the only connection source across Crew, Goals and Flows. */
export function ProjectMcpPanel({workspacePath,canEdit,onAsk,selectedServers,onSelectedServersChange}: {
 view?: 'connected'|'available'; chatSessionId?:string;workspacePath:string;placeNoun:string;canEdit:boolean;onAsk?:(message:string)=>Promise<void>
 selectedServers:string[];onSelectedServersChange:(servers:string[])=>Promise<unknown>|void
}) {
 const vault=useVaultMcpConnections(workspacePath)
 const [error,setError]=useState('')
 const ask=async(message:string)=>{
  try {if(onAsk){await onAsk(message);return}
   const tabs=useChatStore.getState();const active=tabs.activeTabId?tabs.chatTabs[tabs.activeTabId]:undefined
   if(active?.metadata?.agentProfileWorkspace===workspacePath) await openWorkspacePaneChatWithDraft({tabId:active.tabId,message})
   else await openWorkspacePaneChatWithDraft({workspacePath,message})
  } catch(cause){setError(cause instanceof Error?cause.message:'Could not open chat.')}
 }
 const picked=selectedServers.filter(name=>name.startsWith('vault_'))
 return <McpConnectionsPanel servers={vault.servers.map(server=>({...server,
  selection:{label:`Use ${server.name}`,checked:!selectedServers.includes('NO_SERVERS')&&(picked.length===0||picked.includes(server.id)),disabled:!canEdit,change:async()=>{
   const current=selectedServers.includes('NO_SERVERS')?[]:picked.length?picked:vault.servers.map(item=>item.id)
   const next=current.includes(server.id)?current.filter(id=>id!==server.id):[...current,server.id]
   await onSelectedServersChange([...selectedServers.filter(name=>!name.startsWith('vault_')&&name!=='NO_SERVERS'),...(next.length?next:['NO_SERVERS'])])
  }},
  actions:canEdit?[{label:'Manage in chat',run:()=>ask(`Manage the Vault connection ${server.id} for this project. Inspect its saved status and complete any requested setup in chat; reuse this exact account and preserve existing grants.`)}]:[],
 }))} loading={vault.loading} notices={[...vault.notices,...(error?[{message:error}]:[])]} refresh={()=>void vault.refresh()} showTools={false}
  description="All connections are stored in Vault. Add accounts, sign in, update credentials or remove connections through chat."
  addCustom={canEdit?{label:'Connect an app in chat',run:()=>ask('Connect an app to this project using Vault. Guide setup here, including API keys or OAuth app details when needed. Return the real sign-in link, verify discovery and finish selection. Keep existing accounts and permissions.')}:undefined}
 />
}
