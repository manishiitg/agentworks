import { useEffect, useMemo } from 'react'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import { ReportChatRequestController } from './reportChatRequest'

export function useReportChat(workspacePath: string) {
  const controller = useMemo(() => new ReportChatRequestController(workspacePath, async ({ message }) => {
    const tabId = await openProjectAskConversation({ workspacePath }, { activate: false })
    return sendWorkspacePaneMessageToChat({ tabId, message })
  }), [workspacePath])
  useEffect(() => {
    controller.activate()
    return controller.dispose
  }, [controller])
  return controller
}
