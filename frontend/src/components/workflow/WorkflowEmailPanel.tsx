import { GmailNotifications } from './bots/GmailNotifications'
import { useWorkflowBots } from './bots/useWorkflowBots'

export default function WorkflowEmailPanel({ workspacePath }: { workspacePath: string | null }) {
  const settings = useWorkflowBots(workspacePath, undefined, 'email')
  return <GmailNotifications bots={settings} workspacePath={workspacePath} />
}
