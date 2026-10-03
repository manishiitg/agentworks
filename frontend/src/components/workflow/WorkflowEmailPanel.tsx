import { useEffect, useState } from 'react'
import { GmailNotifications } from './bots/GmailNotifications'
import { useWorkflowBots } from './bots/useWorkflowBots'
import { googleAppApi } from '../../api/googleApp'
import { GoogleAccountConnect } from '../../products/work/GoogleAccountConnect'

export default function WorkflowEmailPanel({ workspacePath, scopeNoun = 'workflow', onAsk }: {
  workspacePath: string | null
  scopeNoun?: 'workflow' | 'project' | 'relay'
  onAsk?: (message: string) => void | Promise<void>
}) {
  const settings = useWorkflowBots(workspacePath, undefined, 'email')
  const [googleAppReady, setGoogleAppReady] = useState(false)
  useEffect(() => {
    let cancelled = false
    setGoogleAppReady(false)
    void googleAppApi.status().then(status => { if (!cancelled) setGoogleAppReady(status.configured) }).catch(() => {})
    return () => { cancelled = true }
  }, [workspacePath])
  const privateAccount = /(?:^|\/)Chats\/Code\/projects\//.test(workspacePath || '')
  const platformConnect = googleAppReady && workspacePath
    ? <GoogleAccountConnect key={workspacePath} workspacePath={workspacePath} privateAccount={privateAccount} readOnly={settings.gmailConnectionsReadOnly} onChanged={() => { void settings.loadGmailConnections() }} />
    : undefined
  return <GmailNotifications bots={settings} workspacePath={workspacePath} scopeNoun={scopeNoun} onAsk={onAsk} platformConnect={platformConnect} />
}
