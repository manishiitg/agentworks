import { WorkModelsPanel } from '../work/WorkModelsPanel'
import { useChatStore } from '../../stores/useChatStore'

/** Brain shares the platform's provider, account, model and reasoning controls. */
export function KnowledgebaseModelSettings({ tabId }: { tabId: string | null }) {
  if (!tabId) return null
  return <><p className="mb-4 text-sm text-muted-foreground">Choose the model used for Brain’s access and backup assistant.</p><WorkModelsPanel tabId={tabId} workspacePath="Chats/Knowledgebase" profileId="knowledgebase" profileVersion={1} hideHeader
    onRuntimeChange={selection => {
      useChatStore.getState().setTabMetadata(tabId, {
        agentProfileEngine: selection.engine,
        agentProfileConnectionID: selection.connectionId,
        agentProfileModelID: selection.modelId,
        agentProfileReasoningEffort: selection.reasoningEffort,
        agentProfileRuntimeDirty: true,
      })
    }} /></>
}
