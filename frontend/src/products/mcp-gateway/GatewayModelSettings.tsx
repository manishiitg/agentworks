import { WorkModelsPanel } from '../work/WorkModelsPanel'
import { useChatStore } from '../../stores/useChatStore'

/** CapLayer uses the same provider, account and model panel as Crew and Code. */
export function GatewayModelSettings({ tabId }: { tabId: string | null }) {
  if (!tabId) return null
  return <WorkModelsPanel tabId={tabId} workspacePath="Chats/CapLayer" profileId="caplayer" profileVersion={1} hideHeader
    onRuntimeChange={selection => {
      useChatStore.getState().setTabMetadata(tabId, {
        agentProfileEngine: selection.engine,
        agentProfileConnectionID: selection.connectionId,
        agentProfileModelID: selection.modelId,
        agentProfileReasoningEffort: selection.reasoningEffort,
        agentProfileRuntimeDirty: true,
      })
    }} />
}
