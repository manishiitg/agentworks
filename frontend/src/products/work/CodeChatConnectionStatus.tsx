import { useEffect, useState } from 'react'
import { Laptop } from 'lucide-react'
import api from '../../services/api'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useChatStore } from '../../stores/useChatStore'

export function CodeChatConnectionStatus({ sessionId, workflowPath }: { sessionId: string; workflowPath?: string }) {
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const project = tab?.metadata?.agentProfileProjectId
  const profile = tab?.metadata?.agentProfileId
  const endpoint = laptopSettingsEndpoint(workflowPath, profile, project)
  const [computer, setComputer] = useState('')
  useEffect(() => {
    let active = true
    setComputer('')
    const load = () => {
      if (endpoint) void api.get<{ device_id: string; folders: unknown[] }>(endpoint, { skipSessionContext: true })
        .then(result => { if (active) setComputer(result.data.folders?.length ? result.data.device_id : '') }).catch(() => { if (active) setComputer('') })
    }
    load()
    window.addEventListener('crew-laptop-settings-changed', load)
    return () => { active = false; window.removeEventListener('crew-laptop-settings-changed', load) }
  }, [endpoint])
  if (!computer) return null
  // Opens the Crew's Local laptop panel, the way the model name opens Models.
  return <button type="button" title={`${computer}. Open the Local laptop settings.`} onClick={() => workflowPath ? useWorkflowStore.getState().openWorkspaceView('identity', 'laptop') : window.dispatchEvent(new CustomEvent('agentworks:open-chat-laptop', { detail: { projectId: project } }))}
    className="inline-flex h-7 min-w-0 max-w-48 items-center gap-1.5 rounded px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"><Laptop className="h-3.5 w-3.5 shrink-0" /><span className="truncate max-md:sr-only">Laptop · {computer}</span></button>
}
