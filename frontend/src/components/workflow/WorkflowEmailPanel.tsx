import type { ReactNode } from 'react'
import { GmailNotifications } from './bots/GmailNotifications'
import { useWorkflowBots } from './bots/useWorkflowBots'

export default function WorkflowEmailPanel({ workspacePath, scopeNoun = 'workflow', onAsk, platformConnect }: {
  workspacePath: string | null
  scopeNoun?: 'workflow' | 'project'
  onAsk?: (message: string) => void | Promise<void>
  platformConnect?: ReactNode
}) {
  const settings = useWorkflowBots(workspacePath, undefined, 'email')
  return <GmailNotifications bots={settings} workspacePath={workspacePath} scopeNoun={scopeNoun} onAsk={onAsk} platformConnect={platformConnect} />
}
