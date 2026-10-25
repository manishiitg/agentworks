import { useEffect, useMemo, useState } from 'react'
import { useAuthStore } from '../../../stores/useAuthStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import { ReportChatRequestController } from './reportChatRequest'

export function useReportChat(workspacePath: string) {
  const viewer = useAuthStore(state => state.user?.id)
  const [bound, setBound] = useState<{ workspace: string; viewer?: string; tabId: string } | null>(null)
  const controller = useMemo(() => new ReportChatRequestController(workspacePath, async ({ message }) => {
    const tabId = await openProjectAskConversation({ workspacePath }, { activate: false })
    setBound({ workspace: workspacePath, viewer, tabId })
    return sendWorkspacePaneMessageToChat({ tabId, message })
  }), [workspacePath, viewer])
  useEffect(() => {
    controller.activate()
    return controller.dispose
  }, [controller])
  return { request: controller.request, tabId: bound?.workspace === workspacePath && bound.viewer === viewer ? bound.tabId : null, close: () => setBound(null) }
}
