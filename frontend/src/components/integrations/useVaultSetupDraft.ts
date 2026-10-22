import { useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useVaultChat } from '../../products/mcp-gateway/useVaultChat'

/** One explicit draft path for account and credential setup, without a second form. */
export function useVaultSetupDraft(){
 const vault=useVaultChat(false)
 const [error,setError]=useState('')
 const open=async(message:string,vaultOnly=false)=>{setError('');try{
  const state=useChatStore.getState()
  const candidates=Object.values(state.chatTabs).filter(tab=>!tab.metadata?.isViewOnly&&tab.metadata?.workshopMode!=='run'&&!tab.metadata?.isExecutionRun&&!tab.metadata?.isScheduledRun&&!tab.metadata?.isBotRun&&(tab.metadata?.agentProfileId||tab.metadata?.phaseId==='workflow-builder'))
  const target=candidates.find(tab=>tab.tabId===state.activeTabId)||candidates[0]
  const tabId=(vaultOnly?null:target?.tabId)||await vault.prepare()
  if(!tabId)throw new Error('Open a Crew, Goal or Flow Builder chat to manage your Vault accounts.')
  await openWorkspacePaneChatWithDraft({tabId,message})
 }catch(cause){setError(cause instanceof Error?cause.message:'Could not open setup chat.')}}
 return {open,error:error||vault.error}
}
