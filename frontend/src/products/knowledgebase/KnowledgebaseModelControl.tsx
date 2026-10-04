import { WorkModelsPanel } from '../work/WorkModelsPanel'
import { useChatStore } from '../../stores/useChatStore'

/** Knowledge Base shares the platform's provider, account, model and reasoning controls. */
export function KnowledgebaseModelControl({ tabId }: { tabId: string }) {
  const streaming = useChatStore(state => state.getTabStreamingStatus(tabId))
  return <details className="shrink-0 border-t border-border text-xs">
    <summary className="cursor-pointer px-3 py-2 font-medium text-muted-foreground">Access assistant models</summary>
    <fieldset disabled={streaming} className="max-h-[50vh] overflow-y-auto px-3 pb-3">
      <WorkModelsPanel tabId={tabId} workspacePath="Chats/Knowledgebase" profileId="knowledgebase" profileVersion={1} hideHeader
        onRuntimeChange={selection => {
          useChatStore.getState().setTabMetadata(tabId, {
            agentProfileEngine: selection.engine,
            agentProfileConnectionID: selection.connectionId,
            agentProfileModelID: selection.modelId,
            agentProfileReasoningEffort: selection.reasoningEffort,
            agentProfileRuntimeDirty: true,
          })
        }} />
    </fieldset>
  </details>
}
