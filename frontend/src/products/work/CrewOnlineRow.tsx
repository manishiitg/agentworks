import { WorkspaceContextRow } from '../../components/workspace/WorkspaceContextCard'
import { cloudStateIcon, type CloudState } from './YourComputerPanel'

/** One destination, not an Online group with a second Mac online link. */
export function CrewOnlineRow({ state, theirs = false, onOpen }: { state?: CloudState; theirs?: boolean; onOpen: () => void }) {
  const status = !state ? 'Checking…' : state === 'moving' ? 'Publishing…' : state === 'mac' || state === 'online' ? 'Online' : 'Offline'
  return <WorkspaceContextRow icon={cloudStateIcon(state === 'offline' ? 'mac_off' : state, theirs)} detail={status} onClick={onOpen}>Mac online</WorkspaceContextRow>
}
