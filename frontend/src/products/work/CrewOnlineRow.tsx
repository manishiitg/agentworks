import { WorkspaceContextRow } from '../../components/workspace/WorkspaceContextCard'
import { cloudStateIcon, type CloudState } from './YourComputerPanel'

/** One destination: whether this project opens from your phone and the website ("Remote Control", On or Off). */
// compact (shared line with AgentBridge): On/Off as a small pill kept whole beside the label; the label truncates first.
export function CrewOnlineRow({ state, theirs = false, compact = false, onOpen }: { state?: CloudState; theirs?: boolean; compact?: boolean; onOpen: () => void }) {
  const on = state === 'mac' || state === 'online'
  const status = !state ? 'Checking…' : on ? 'On' : 'Off'
  const hint = state && !on ? 'Turn on to use this project from your phone' : undefined
  return <WorkspaceContextRow icon={cloudStateIcon(state === 'offline' ? 'mac_off' : state, theirs)} detail={compact ? undefined : status} badge={compact ? <span className={`rounded-full px-1.5 py-px text-[10px] leading-none ${on ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground'}`}>{state ? status : '…'}</span> : undefined} hint={compact ? `Remote Control: ${status}${hint ? `. ${hint}` : ''}` : hint} onClick={onOpen}>Remote Control</WorkspaceContextRow>
}
