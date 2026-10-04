import { GmailNotifications } from './bots/GmailNotifications'
import { useWorkflowBots } from './bots/useWorkflowBots'
import { GoogleAccountConnect } from '../../products/work/GoogleAccountConnect'

export default function WorkflowEmailPanel({ workspacePath, scopeNoun = 'workflow', onAsk }: {
  workspacePath: string | null
  scopeNoun?: 'workflow' | 'project' | 'relay'
  onAsk?: (message: string) => void | Promise<void>
}) {
  const settings = useWorkflowBots(workspacePath, undefined, 'email')
  const privateAccount = /(?:^|\/)Chats\/Code\/projects\//.test(workspacePath || '')
  const platformConnect = workspacePath
    ? <GoogleAccountConnect key={workspacePath} workspacePath={workspacePath} privateAccount={privateAccount} readOnly={settings.gmailConnectionsReadOnly} onChanged={() => { void settings.loadGmailConnections() }} />
    : undefined
  return <GmailNotifications bots={settings} workspacePath={workspacePath} scopeNoun={scopeNoun} onAsk={onAsk} platformConnect={platformConnect} />
}
