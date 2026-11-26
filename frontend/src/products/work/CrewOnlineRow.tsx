import { WorkspaceContextRow } from '../../components/workspace/WorkspaceContextCard'
import { cloudStateIcon, type CloudState } from './YourComputerPanel'

/** One destination: whether this project opens from your phone and the website ("Remote Control", On or Off). */
export function CrewOnlineRow({ state, theirs = false, onOpen }: { state?: CloudState; theirs?: boolean; onOpen: () => void }) {
  const on = state === 'mac' || state === 'online'
  const status = !state ? 'Checking…' : on ? 'On' : 'Off'
  return <WorkspaceContextRow icon={cloudStateIcon(state === 'offline' ? 'mac_off' : state, theirs)} detail={status} hint={state && !on ? 'Turn on to use this project from your phone' : undefined} onClick={onOpen}>Remote Control</WorkspaceContextRow>
}
