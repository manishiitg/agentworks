import { ArrowLeft, ArrowRight, AppWindow, Eye, LayoutDashboard, MessageCircle, Slack, Users, Webhook, type LucideIcon } from 'lucide-react'
import { ShareDashboardButton } from '../workflow/ShareDashboardButton'

export type UseShareChannel = 'slack-app' | 'whatsapp' | 'cli'

/** Navigation over existing project sharing and channel adapters; grants no access. */
export function UseSharePanel({ workspacePath, slack, whatsapp, onChannel, onDashboard, onApi, onAccess }: {
  workspacePath: string
  slack: boolean
  whatsapp: boolean
  onChannel: (channel: UseShareChannel) => void
  onDashboard?: () => void
  onApi?: () => void
  onAccess?: () => void
}) {
  const channels: { id: UseShareChannel; label: string; description: string; icon: LucideIcon }[] = [
    ...(slack ? [{ id: 'slack-app' as const, label: 'Slack app', description: 'Let people message this agent in Slack.', icon: Slack }] : []),
    ...(whatsapp ? [{ id: 'whatsapp' as const, label: 'WhatsApp', description: 'Let people reach this agent through WhatsApp.', icon: MessageCircle }] : []),
    { id: 'cli', label: 'MCP access', description: 'Use this project from Claude, ChatGPT or your CLI.', icon: AppWindow },
  ]
  return <div className="space-y-5" aria-label="Use and share this project">
    <section className="rounded-xl border border-border p-4">
      <div className="flex items-center gap-2"><Users aria-hidden="true" className="h-4 w-4 text-muted-foreground" /><h3 className="text-sm font-semibold">People &amp; access</h3></div>
      <p className="mt-2 text-xs leading-5 text-muted-foreground">Share the dashboard, let teammates use the agent and its published functions, and give them read access to project files and results.</p>
      <p className="mt-2 flex items-center gap-1.5 text-[11px] text-muted-foreground"><Eye aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />Sharing keeps the existing project permissions. Secret values stay private.</p>
      <div className="mt-3"><ShareDashboardButton key={workspacePath} workspacePath={workspacePath} inline label="Manage people & access" /></div>
      {onAccess && <button type="button" onClick={onAccess} className="mt-3 text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground">Project roles &amp; permissions</button>}
    </section>
    <section aria-label="Where people can use this project" className="space-y-1">
      <h3 className="mb-2 text-xs font-medium text-muted-foreground">Where people can use it</h3>
      {onDashboard && <Destination icon={LayoutDashboard} label="Dashboard" description="Open the shared visual workspace." onClick={onDashboard} />}
      {channels.map(channel => <Destination key={channel.id} {...channel} onClick={() => onChannel(channel.id)} />)}
      {onApi && <Destination icon={Webhook} label="API access" description="Run this Flow from another app through its existing triggers." onClick={onApi} />}
    </section>
    <p className="text-[11px] leading-5 text-muted-foreground">Channels don’t grant access by themselves. People sign in or use the channel’s existing access rules.</p>
  </div>
}

function Destination({ icon: Icon, label, description, onClick }: { icon: LucideIcon; label: string; description: string; onClick: () => void }) {
  return <button type="button" onClick={onClick} className="flex w-full items-center gap-3 rounded-lg px-3 py-3 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1"><span className="block text-xs font-medium">{label}</span><span className="mt-0.5 block text-[11px] leading-4 text-muted-foreground">{description}</span></span><ArrowRight aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
  </button>
}

export function UseShareBack({ onBack, label }: { onBack: () => void; label: string }) {
  return <button type="button" onClick={onBack} className="mt-2 inline-flex items-center gap-1.5 rounded py-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft aria-hidden="true" className="h-3.5 w-3.5" />Use &amp; share<span aria-hidden="true">/</span><span className="text-foreground">{label}</span></button>
}
