import { GmailNotifications } from './bots/GmailNotifications'
import { useWorkflowBots } from './bots/useWorkflowBots'

export default function WorkflowEmailPanel({ workspacePath, scopeNoun = 'workflow', onAsk }: {
  workspacePath: string | null
  scopeNoun?: 'workflow' | 'project' | 'relay'
  onAsk?: (message: string) => void | Promise<void>
}) {
  const settings = useWorkflowBots(workspacePath, undefined, 'email')
  return <GmailNotifications bots={settings} workspacePath={workspacePath} scopeNoun={scopeNoun} onAsk={onAsk} />
}
