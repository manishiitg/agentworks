import { WorkspaceContextRow } from '../../components/workspace/WorkspaceContextCard'
import { cloudStateIcon, type CloudState } from './YourComputerPanel'

/** One destination: whether this project opens from your phone and the website ("Remote Control", On or Off). */
// compact: no On/Off word (the icon's dot already shows it); the status moves to the tooltip.
export function CrewOnlineRow({ state, theirs = false, compact = false, onOpen }: { state?: CloudState; theirs?: boolean; compact?: boolean; onOpen: () => void }) {
  const on = state === 'mac' || state === 'online'
  const status = !state ? 'Checking…' : on ? 'On' : 'Off'
  const hint = state && !on ? 'Turn on to use this project from your phone' : undefined
  return <WorkspaceContextRow icon={cloudStateIcon(state === 'offline' ? 'mac_off' : state, theirs)} detail={compact ? undefined : status} hint={compact ? `Remote Control: ${status}${hint ? `. ${hint}` : ''}` : hint} onClick={onOpen}>Remote Control</WorkspaceContextRow>
}
