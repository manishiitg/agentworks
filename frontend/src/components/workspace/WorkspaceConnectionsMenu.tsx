import { Portal as TooltipPortal } from '@radix-ui/react-tooltip'
import { Bot, Cable, Chrome, Grid2X2, Monitor, Slack, type LucideIcon } from 'lucide-react'
import { useBrowserToolbarConnection } from '../../hooks/useBrowserToolbarConnection'
import type { WorkspaceStatusRow } from '../../products/work/useWorkspaceStatus'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { WorkspaceCompactGroup } from './WorkspaceCompactGroup'
import { WorkspaceContextRow } from './WorkspaceContextCard'

function ConnectionIcon({ icon: Icon, hint, connected, attention, count, onClick }: {
  icon: LucideIcon; hint: string; connected?: boolean; attention?: boolean; count?: number; onClick: () => void
}) {
  return <Tooltip><TooltipTrigger asChild>
    <button type="button" aria-label={hint} onClick={onClick} className="relative flex h-8 min-w-7 shrink-0 items-center justify-center gap-1 rounded-md px-1 text-muted-foreground hover:bg-background/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <Icon aria-hidden="true" className="h-3.5 w-3.5"/>
      {count !== undefined && <span className="text-[10px]">{count}</span>}
      <span aria-hidden="true" className={`absolute right-0.5 top-1 h-1.5 w-1.5 rounded-full ${attention ? 'bg-amber-500' : connected ? 'bg-emerald-500' : 'bg-muted-foreground/50'}`}/>
    </button>
  </TooltipTrigger><TooltipPortal><TooltipContent className="max-w-72 break-words">{hint}</TooltipContent></TooltipPortal></Tooltip>
}
export function WorkspaceConnectionsMenu({ workspacePath, profileId, browserAllowed, mcpAllowed, apps, loading, unavailable, onOpen }: {
  workspacePath: string; profileId: string; browserAllowed: boolean; mcpAllowed: boolean
  apps: WorkspaceStatusRow[]; loading: boolean; unavailable: boolean; onOpen: (destination: string) => void
}) {
  const browser = useBrowserToolbarConnection(workspacePath, profileId, browserAllowed)
  const mcps = apps.filter(item => item.destination === 'mcp:connected')
  const accounts = apps.filter(item => item.destination !== 'mcp:connected')
  const attention = mcps.filter(item => item.attention).length
  const mcpDetail = loading ? 'Checking…' : unavailable ? 'Unavailable' : `${mcps.length}${attention ? ` · ${attention} need attention` : ''}`
  const mcpHint = `MCPs · ${mcpDetail}`
  const browserHint = `Browser · ${browser.name ? `${browser.name} · ` : ''}${browser.label}`
  const browserIcon = browser.name?.toLowerCase().includes('chrome') ? Chrome : Monitor
  const labelFor = (item: WorkspaceStatusRow) => item.destination === 'mcp:slack-bot' ? 'Slack bot' : item.label
  const iconFor = (item: WorkspaceStatusRow) => item.destination === 'mcp:gmail' ? Grid2X2 : item.destination === 'mcp:slack-bot' ? Bot : Slack
  if (!browserAllowed && !mcpAllowed && !accounts.length) return null
  return <TooltipProvider delayDuration={200}><WorkspaceCompactGroup title="Connections" icon={Cable} preview={<>
    {browserAllowed && <ConnectionIcon icon={browserIcon} hint={browserHint} connected={browser.connected} onClick={() => onOpen('browser')}/>}
    {accounts.map(item => <ConnectionIcon key={item.id} icon={iconFor(item)} hint={`${labelFor(item)} · ${item.detail}`} connected={item.connected} attention={item.attention} onClick={() => onOpen(item.destination)}/>)}
    {mcpAllowed && <ConnectionIcon icon={Cable} hint={mcpHint} count={loading || unavailable ? undefined : mcps.length} attention={attention > 0 || unavailable} connected={!loading && !unavailable && mcps.length > 0 && mcps.every(item => item.connected)} onClick={() => onOpen('mcp:connected')}/>}
  </>}>
    {browserAllowed && <WorkspaceContextRow icon={browserIcon} hint={browserHint} detail={browser.name || browser.label} attention={!browser.connected && browser.label !== 'Checking…'} onClick={() => onOpen('browser')}>Browser</WorkspaceContextRow>}
    {accounts.map(item => <WorkspaceContextRow key={item.id} icon={iconFor(item)} hint={`${labelFor(item)} · ${item.detail}`} detail={item.detail} attention={item.attention} onClick={() => onOpen(item.destination)}>{labelFor(item)}</WorkspaceContextRow>)}
    {mcpAllowed && <WorkspaceContextRow icon={Cable} hint={mcpHint} detail={mcpDetail} attention={attention > 0 || unavailable} onClick={() => onOpen('mcp:connected')}>MCPs</WorkspaceContextRow>}
  </WorkspaceCompactGroup></TooltipProvider>
}
