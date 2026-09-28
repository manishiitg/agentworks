import { AlertCircle, ArrowLeft, MessageSquare, Phone } from 'lucide-react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { useWorkflowBots, type BotRouteTarget } from './bots/useWorkflowBots'
import { SlackSetup } from './bots/SlackSetup'
import { WhatsAppSetup } from './bots/WhatsAppSetup'
import { RouteChip } from './bots/RouteChips'
import { ChannelRow } from './bots/AddChannel'
import { routeId } from './bots/types'
import { AskAIButton } from './AskAIButton'
import { WorkspaceViewTabs } from './WorkspaceViewTabs'

type WorkflowBotsPanelProps = {
  workspacePath: string | null
  target?: BotRouteTarget
  scopeNoun?: 'workflow' | 'project'
  onAsk?: (message: string) => void | Promise<void>
  /**
   * Pins the panel to one channel with no tab row. The Integrations view
   * mounts one pinned panel per top-level tab; standalone hosts (the Work
   * project pane) omit it and get the Slack/WhatsApp tabs.
   */
  fixedChannel?: BotChannel
  /** Slack: only this target's own app, 1:1 direct messages (a Code). */
  ownBotOnly?: boolean
}

type BotChannel = 'slack' | 'whatsapp'

const CHANNEL_TABS: Array<{ value: BotChannel; label: string }> = [
  { value: 'slack', label: 'Slack' },
  { value: 'whatsapp', label: 'WhatsApp' },
]

// Composition over useWorkflowBots: status card, route chips, add-channel
// rows, and the Slack/WhatsApp drill-ins. Never owns a scroll container:
// every host (the Integrations Bots tab, the Work project pane) scrolls.
export default function WorkflowBotsPanel({ workspacePath, target, scopeNoun = 'workflow', onAsk, fixedChannel, ownBotOnly = false }: WorkflowBotsPanelProps) {
  const bots = useWorkflowBots(workspacePath, target, 'bots')
  const { setup, setSetup, workflowId, workflowRoutes, routeError, waRoutingError } = bots
  const [internalChannel, setInternalChannel] = usePersistentTab<BotChannel>('agentworks.tab.bots-channel', 'slack', ['slack', 'whatsapp'])
  const channel = fixedChannel ?? internalChannel

  const askSlackSetup = (
    <AskAIButton
      workspacePath={workspacePath}
      onAsk={onAsk}
      label={ownBotOnly ? 'Ask AI to set up Slack' : scopeNoun === 'project' ? 'Ask Crew to set up Slack' : 'Ask Builder to set up Slack'}
      message={ownBotOnly
        ? `Help me set up this ${scopeNoun}'s own Slack bot. It answers 1:1 direct messages only, never in channels, and never through the shared bot. Guide me through creating the Slack app (with the im:history, im:read and users:read.email scopes and the App Home Messages tab) and entering its credentials in the settings UI, without requesting tokens in chat.`
        : `Read builder-reference/references/slack-bot-routing.md, inspect get_slack_bot_settings, and help me set up Slack for this ${scopeNoun}. First ask whether it should have its own bot (a Slack app that answers only for this ${scopeNoun}, in any channel it is invited to, with no routes) or use the shared bot in specific channels. For its own bot, guide me through app creation and credentials in the settings UI without requesting tokens in chat. For the shared bot, use the existing scoped tools to create the route once the channel ID is clear; use run mode without asking for a grant. Everyone in the channel is allowed by default; ask about blocked emails only if I need exclusions.`}
    />
  )

  // Publishing the bot's App Home tab needs the owner's full-mode chat: only
  // there does the slack tool take any Slack API method (views.publish).
  const askSlackHomeTab = (
    <AskAIButton
      workspacePath={workspacePath}
      onAsk={onAsk}
      label="Ask AI to publish the Home tab"
      message={`Design and publish this ${scopeNoun}'s Slack bot App Home tab (what people see when they open the app in Slack). First read what this ${scopeNoun} does, then draft a Block Kit home view: a one-line intro, what it can help with, three example questions, and how to reach it (mention it in a channel, or DM it). Show me the draft and wait for my OK. Then publish it with the slack tool: method views.publish, parameters user_id and view (type "home"). Find my Slack user ID with users.lookupByEmail (ask me for my Slack email if you do not know it). If Slack refuses, tell me exactly why; a disabled Home tab means turning on App Home > Home Tab in the Slack app.`}
    />
  )

  if (setup !== null) {
    return (
      <div className="flex flex-col gap-4">
        <div className="shrink-0 space-y-4">
          <button
            type="button"
            onClick={() => setSetup(null)}
            className="inline-flex items-center gap-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" />
            Back
          </button>
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
            {setup === 'slack' ? <MessageSquare className="h-4 w-4" /> : <Phone className="h-4 w-4" />}
            {setup === 'slack' ? 'Slack' : 'WhatsApp'}
            {setup !== 'slack' && <span className="text-xs font-normal text-muted-foreground">· shared across AgentWorks</span>}
          </div>
        </div>
        {setup === 'slack' ? <SlackSetup bots={bots} headerAction={askSlackSetup} homeTabAction={askSlackHomeTab} /> : <WhatsAppSetup bots={bots} />}
      </div>
    )
  }

  const routes = workflowRoutes.filter(route => route.kind === channel)
  const tabs = fixedChannel === undefined && (
    <div className="border-b border-border">
      <WorkspaceViewTabs value={channel} onChange={(value: string) => setInternalChannel(value as BotChannel)} options={CHANNEL_TABS} ariaLabel="Bots" />
    </div>
  )

  // Slack is one question (own bot vs shared bot); SlackSetup owns the
  // whole tab. WhatsApp keeps the status row + route chips.
  if (channel === 'slack') {
    return (
      <div className="space-y-4">
        {tabs}
        <SlackSetup bots={bots} headerAction={askSlackSetup} homeTabAction={askSlackHomeTab} ownBotOnly={ownBotOnly} />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {tabs}
      <section className="overflow-hidden rounded-md border border-border bg-background">
        <ChannelRow bots={bots} kind={channel} manageRoutes />
        <div className="space-y-2 px-3 pb-3">
          <h3 className="text-xs font-medium text-muted-foreground">{target ? `Routes for this ${scopeNoun}` : 'Workflow routes'}</h3>
          {routes.length > 0 ? (
            <div className="grid gap-2">
              {routes.map(route => <RouteChip key={routeId(route)} bots={bots} route={route} />)}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">{workflowId ? 'No routes yet.' : `Select a ${scopeNoun} to manage routes.`}</p>
          )}
          {routeError && <p className="flex items-start gap-1.5 text-xs text-red-600 dark:text-red-400"><AlertCircle className="h-3.5 w-3.5 shrink-0" />{routeError}</p>}
          {waRoutingError && <p className="text-xs text-amber-600 dark:text-amber-400">WhatsApp routing unavailable: {waRoutingError}</p>}
        </div>
      </section>
    </div>
  )
}
