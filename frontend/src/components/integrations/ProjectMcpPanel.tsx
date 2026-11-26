import { useCallback, useEffect, useRef, useState } from 'react'
import { McpConnectionsPanel } from './McpConnectionsPanel'
import { useVaultMcpConnections } from './useVaultMcpConnections'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { vaultConnectionsApi, type VaultCatalogApp } from '../../api/vaultConnections'
import { useChatStore } from '../../stores/useChatStore'
import { AddToTeamControl, useAddToTeam } from '../../products/work/TeamWay'
import { loadProjectVault, selectProjectVaultItem, type ProjectVaultInventory } from '../../api/projectVault'
import { LocalMcpImport, useLocalMcpServers } from '../../products/work/LocalMcpImport'

/** Vault is the only connection source across Crew, Goals and Flows. */
export function ProjectMcpPanel({view='connected',workspacePath,tabId,canEdit,onAsk,onSelectionSaved}: {
 view?: 'connected'|'available'; chatSessionId?:string;workspacePath:string;placeNoun:string;canEdit:boolean;onAsk?:(message:string)=>Promise<void>
 tabId?: string
 onSelectionSaved?: (servers:string[])=>void
}) {
 const vault=useVaultMcpConnections(workspacePath)
 const projectId=useChatStore(state=>tabId?state.chatTabs[tabId]?.metadata?.agentProfileProjectId:undefined)
 const team=useAddToTeam(projectId)
 const [error,setError]=useState('')
 const [selection,setSelection]=useState<{root:string; inventory:ProjectVaultInventory}>()
 const generation=useRef(0)
 const refreshSelection=useCallback(async()=>{const version=++generation.current;try{const inventory=await loadProjectVault(workspacePath);if(version===generation.current){setSelection({root:workspacePath,inventory});setError('')}}catch{if(version===generation.current)setError('Project app selections could not be loaded. Refresh to try again.')}},[workspacePath])
 useEffect(()=>{void refreshSelection();const focus=()=>void refreshSelection();window.addEventListener('focus',focus);return()=>{generation.current++;window.removeEventListener('focus',focus)}},[refreshSelection])
 const streaming=useChatStore(state=>Boolean(tabId&&state.chatTabs[tabId]?.isStreaming))
 const wasStreaming=useRef(false)
 useEffect(()=>{if(wasStreaming.current&&!streaming){void vault.refresh();void refreshSelection()}wasStreaming.current=streaming},[streaming,vault.refresh,refreshSelection])
 // The person's own MCP servers from Claude, Codex, Cursor and agy (Mac app, project owner only; null elsewhere).
 const localMcp=useLocalMcpServers({workspacePath},canEdit)
 const [catalog,setCatalog]=useState<VaultCatalogApp[]>([])
 useEffect(()=>{if(view!=='available')return;let current=true;void vaultConnectionsApi.catalog().then(apps=>{if(current)setCatalog(apps)}).catch(()=>{if(current)setError('Could not load the app catalog. You can still connect an app through chat.')});return()=>{current=false}},[view])
 const ask=async(message:string)=>{
  try {if(onAsk){await onAsk(message);return}
   const tabs=useChatStore.getState();const active=tabs.activeTabId?tabs.chatTabs[tabs.activeTabId]:undefined
   if(active?.metadata?.agentProfileWorkspace===workspacePath) await openWorkspacePaneChatWithDraft({tabId:active.tabId,message})
   else await openWorkspacePaneChatWithDraft({workspacePath,message})
  } catch(cause){setError(cause instanceof Error?cause.message:'Could not open chat.')}
 }
 const picked=selection?.root===workspacePath?selection.inventory.vaults.flatMap(v=>v.items):[]
 return <McpConnectionsPanel view={view} catalog={catalog.map(app=>({id:app.name,name:app.name,description:app.oauth?'Sign in with your account':'Connect an account',connect:canEdit?{label:'Connect in chat',run:()=>ask(`Connect ${app.name} to this project. Use this exact catalog provider, guide credential or OAuth setup here, return the real sign-in URL and verify discovery. Preserve existing accounts and permissions.`)}:undefined}))} servers={vault.servers.map(server=>{const own=picked.find(item=>item.kind==='mcp'&&`vault_${item.id}`===server.id&&item.source==='you');return({...server,
  controls:team&&own?<AddToTeamControl team={team} kind="app" id={own.id} provider={own.provider} signIn={own.sign_in} />:undefined,
  selection:{label:`Use ${server.name}`,checked:picked.find(item=>item.kind==='mcp'&&`vault_${item.id}`===server.id)?.selected??false,disabled:!canEdit||!picked.some(item=>item.kind==='mcp'&&`vault_${item.id}`===server.id&&item.can_select),change:async()=>{
   const item=picked.find(item=>item.kind==='mcp'&&`vault_${item.id}`===server.id)
   if(!item)return
   try{const inventory=await selectProjectVaultItem(workspacePath,item);setSelection({root:workspacePath,inventory});onSelectionSaved?.(inventory.selected_servers);setError('')}catch(cause){setError(cause instanceof Error?cause.message:'Could not save this app selection.')}
  }},
  actions:canEdit?[{label:'Manage in chat',run:()=>ask(`Manage the saved connection ${server.id} for this project. Inspect its saved status and complete any requested setup in chat; reuse this exact account and preserve existing grants.`)}]:[],
 })})} loading={vault.loading} notices={[...vault.notices,...(team?.line?[{message:team.line}]:[]),...(error?[{message:error}]:[])]} refresh={()=>{void vault.refresh();void refreshSelection()}} showTools
  description="Choose the apps this project can use. Open an app to see its tools, or add another account."
  children={canEdit&&localMcp.servers?.length?<details className="rounded-md border border-border p-3" data-testid="local-mcp-import-section"><summary className="cursor-pointer text-sm font-medium">Import from Claude, Codex, Cursor or agy ({localMcp.servers.length})</summary><p className="mb-3 mt-1 text-xs text-muted-foreground">MCP servers you set up in those apps. Chats here don't load them on their own.</p><LocalMcpImport key={localMcp.servers.map(s=>s.id+(s.in_vault?'1':'')).join()} servers={localMcp.servers} scope={{workspacePath}} onImported={()=>{void vault.refresh();void refreshSelection()}} /></details>:undefined}
  addCustom={canEdit?{label:'Connect an app in chat',run:()=>ask('Connect an app to this project. Guide setup here, including API keys or OAuth app details when needed. Return the real sign-in link, verify discovery and finish selection. Keep existing accounts and permissions.')}:undefined}
 />
}
