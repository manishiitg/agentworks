import { Monitor } from 'lucide-react'
import { useBrowserToolbarConnection } from '../../hooks/useBrowserToolbarConnection'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'

/** The Browser entry shows the same connection the Browser panel reports. */
export function WorkspaceBrowserRow({ workspacePath, profileId, onClick }: { workspacePath: string; profileId: string; onClick: () => void }) {
  const connected = useBrowserToolbarConnection(workspacePath, profileId)
  return <Row icon={Monitor} detail={connected ? 'Connected' : 'Not connected'} onClick={onClick}>Browser</Row>
}
