import { Portal as TooltipPortal } from '@radix-ui/react-tooltip'
import { Bot, Cable, ChevronRight, Chrome, Github, Grid2X2, MessageCircle, Monitor, MonitorCog, Settings2, Slack, type LucideIcon } from 'lucide-react'
import { useBrowserToolbarConnection } from '../../hooks/useBrowserToolbarConnection'
import type { WorkspaceStatusRow } from '../../products/work/useWorkspaceStatus'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { WorkspaceCompactGroup } from './WorkspaceCompactGroup'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { connectionSummary } from './workspaceConnectionStatus'

function ConnectionIcon({ icon: Icon, hint, count, onClick }: {
  icon: LucideIcon; hint: string; count?: number; onClick: () => void
}) {
  return <Tooltip><TooltipTrigger asChild>
    <button type="button" aria-label={hint} onClick={onClick} className="relative flex h-6 min-w-6 shrink-0 items-center justify-center gap-0.5 rounded-full border border-border/60 bg-muted px-1.5 text-muted-foreground hover:z-10 hover:bg-accent hover:text-foreground focus-visible:z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:bg-zinc-800 dark:hover:bg-zinc-700">
      <Icon aria-hidden="true" className="h-3 w-3"/>
      {count !== undefined && <span className="text-[10px]">{count}</span>}
    </button>
  </TooltipTrigger><TooltipPortal><TooltipContent className="max-w-72 break-words">{hint}</TooltipContent></TooltipPortal></Tooltip>
}
export function WorkspaceConnectionsMenu({ workspacePath, profileId, browserAllowed, mcpAllowed, apps, loading, unavailable, onOpen, onManage }: {
  workspacePath: string; profileId: string; browserAllowed: boolean; mcpAllowed: boolean
  apps: WorkspaceStatusRow[]; loading: boolean; unavailable: boolean; onOpen: (destination: string) => void; onManage?: () => void
}) {
  const browser = useBrowserToolbarConnection(workspacePath, profileId, browserAllowed)
  const mcps = apps.filter(item => item.destination === 'mcp:connected')
  const connectedMcps = mcps.filter(item => item.connected)
  const accounts = apps.filter(item => item.destination !== 'mcp:connected')
  const attention = mcps.filter(item => item.attention).length
  const mcpDetail = unavailable ? 'Unavailable' : loading ? 'Checking…' : connectionSummary(mcps, true)
  const mcpHint = `MCPs · ${mcpDetail}`
  const browserHint = `Browser · ${browser.name ? `${browser.name} · ` : ''}${browser.label}`
  const browserIcon = browser.name?.toLowerCase().includes('chrome') ? Chrome : Monitor
  const labelFor = (item: WorkspaceStatusRow) => item.destination === 'mcp:slack-bot' ? 'Slack bot' : item.label
  const iconFor = (item: WorkspaceStatusRow) => item.destination === 'mcp:computer-use' ? MonitorCog : item.destination === 'mcp:github' ? Github : item.destination === 'mcp:gmail' ? Grid2X2 : item.destination === 'mcp:slack-bot' ? Bot : item.destination === 'mcp:whatsapp' ? MessageCircle : Slack
  if (!browserAllowed && !mcpAllowed && !accounts.length && !onManage) return null
  return <TooltipProvider delayDuration={200}><WorkspaceCompactGroup title="Connections" icon={Cable} preview={<div className="isolate flex -space-x-1.5">
    {browserAllowed && browser.connected && <ConnectionIcon icon={browserIcon} hint={browserHint} onClick={() => onOpen('browser')}/>}
    {accounts.filter(item => item.connected).map(item => <ConnectionIcon key={item.id} icon={iconFor(item)} hint={`${labelFor(item)} · ${item.detail}`} onClick={() => onOpen(item.destination)}/>)}
    {mcpAllowed && !loading && !unavailable && connectedMcps.length > 0 && <ConnectionIcon icon={Cable} hint={mcpHint} count={connectedMcps.length} onClick={() => onOpen('mcp:connected')}/>}
  </div>}>
    {browserAllowed && <WorkspaceContextRow icon={browserIcon} hint={browserHint} detail={browser.name || browser.label} attention={!browser.connected && browser.label !== 'Checking…'} onClick={() => onOpen('browser')}>Browser</WorkspaceContextRow>}
    {accounts.map(item => <WorkspaceContextRow key={item.id} icon={iconFor(item)} hint={`${labelFor(item)} · ${item.detail}`} detail={item.detail} attention={item.attention} onClick={() => onOpen(item.destination)}>{labelFor(item)}</WorkspaceContextRow>)}
    {mcpAllowed && <WorkspaceContextRow icon={Cable} hint={mcpHint} detail={mcpDetail} attention={attention > 0 || unavailable} onClick={() => onOpen('mcp:connected')}>MCPs</WorkspaceContextRow>}
    {onManage && <WorkspaceContextRow icon={Settings2} detail={<ChevronRight aria-hidden="true" className="h-3 w-3" />} onClick={onManage}>Manage integrations</WorkspaceContextRow>}
  </WorkspaceCompactGroup></TooltipProvider>
}
