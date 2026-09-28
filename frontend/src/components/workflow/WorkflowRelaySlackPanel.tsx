import { useState } from 'react'
import { Button } from '../ui/Button'
import { FormSection } from '../ui/FormSection'
import { Input } from '../ui/Input'
import { Label } from '../ui/label'
import { SecretField } from '../ui/SecretField'
import { StatusBanner } from './bots/StatusBanner'
import { useWorkflowBots } from './bots/useWorkflowBots'

// Relay shares the workflow Slack connection and tool plumbing. Its graph has
// no inbound bot turn, so this tab only configures outbound Slack access.
export default function WorkflowRelaySlackPanel({ workspacePath }: { workspacePath: string | null }) {
  const bots = useWorkflowBots(workspacePath, undefined, 'slack')
  const [editing, setEditing] = useState(false)
  const connections = (bots.slackOriginal.connections || []).filter(connection =>
    connection.id === bots.slackSelection.own?.id,
  )
  const selected = bots.slackSelection.selectionId
  const own = bots.slackSelection.own
  const canEdit = bots.canManageWorkflowSlack && !!workspacePath

  if (bots.slackLoading) return <p className="text-sm text-muted-foreground">Loading Slack connection…</p>

  return (
    <div className="space-y-4">
      {bots.slackError && <StatusBanner tone="error">{bots.slackError}</StatusBanner>}
      {bots.slackSuccess && <StatusBanner tone="success">{bots.slackSuccess}</StatusBanner>}
      <FormSection
        title="Slack app"
        description="Choose the Slack app Relay agents use to send messages. Run notifications can also be delivered through Slack."
      >
        <div className="space-y-2">
          <Label htmlFor="relay-slack-connection">Connection</Label>
          <select
            id="relay-slack-connection"
            value={selected}
            onChange={event => { void bots.selectWorkflowSlackConnection(event.target.value || null) }}
            disabled={!canEdit || bots.slackConnSaving}
            className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm text-foreground"
          >
            <option value="">Platform default{bots.slackSelection.effective && !selected ? ` · ${bots.slackSelection.effective.display_name}` : ''}</option>
            {connections.map(connection => (
              <option key={connection.id} value={connection.id}>
                {connection.display_name}{connection.configured && connection.enabled ? ' · Ready' : ' · Needs setup'}
              </option>
            ))}
          </select>
          {selected && !bots.slackSelection.effective && <p className="text-xs text-amber-600">The selected Slack app is unavailable. Choose another connection.</p>}
        </div>
        {own && !editing ? (
          <div className="flex items-center gap-2">
            <Button variant="outline" onClick={() => { void bots.testWorkflowSlackConnection() }} disabled={!canEdit || bots.slackConnTesting}>
              {bots.slackConnTesting ? 'Testing…' : 'Test app'}
            </Button>
            <Button variant="outline" onClick={() => setEditing(true)} disabled={!canEdit}>Edit own app</Button>
            <Button variant="outline" onClick={() => { void bots.removeWorkflowSlackConnection() }} disabled={!canEdit || bots.slackConnSaving} className="ml-auto">
              {bots.slackConnConfirmDelete ? 'Click again to remove' : 'Remove own app'}
            </Button>
          </div>
        ) : (
          <div className="space-y-3 border-t border-border pt-3">
            <p className="text-sm font-medium text-foreground">{own ? 'Edit Relay app' : 'Add a Relay app'}</p>
            <p className="text-xs text-muted-foreground">Use a Slack app with a bot token. Add an app token when Socket Mode is enabled for that app. Tokens stay in the connection settings.</p>
            <div className="space-y-1"><Label htmlFor="relay-slack-name">App name</Label><Input id="relay-slack-name" value={bots.slackConnName} onChange={event => bots.setSlackConnName(event.target.value)} disabled={!canEdit} placeholder="Relay Slack" /></div>
            <SecretField label="Bot token" value={bots.slackConnBot} onChange={bots.setSlackConnBot} disabled={!canEdit} placeholder="xoxb-…" />
            <SecretField label="App token (optional)" value={bots.slackConnApp} onChange={bots.setSlackConnApp} disabled={!canEdit} placeholder="xapp-…" />
            <div className="flex gap-2">
              <Button onClick={() => { void bots.saveWorkflowSlackConnection().then(id => { if (id) setEditing(false) }) }} disabled={!canEdit || !bots.slackConnHasChanges || bots.slackConnSaving}>Save app</Button>
              {own && <Button variant="outline" onClick={() => setEditing(false)}>Cancel</Button>}
            </div>
          </div>
        )}
        {bots.slackConnTestResult && <p className="text-xs text-muted-foreground">{bots.slackConnTestResult.message}</p>}
      </FormSection>
    </div>
  )
}
