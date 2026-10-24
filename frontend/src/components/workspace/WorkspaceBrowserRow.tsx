import { Monitor } from 'lucide-react'
import { useBrowserToolbarConnection } from '../../hooks/useBrowserToolbarConnection'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'

/** The Browser entry shows the same connection the Browser panel reports. */
export function WorkspaceBrowserRow({ workspacePath, profileId, onClick }: { workspacePath: string; profileId: string; onClick: () => void }) {
  const status = useBrowserToolbarConnection(workspacePath, profileId)
  return <Row icon={Monitor} detail={status.label} onClick={onClick}>Browser</Row>
}
