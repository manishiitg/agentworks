import { Monitor } from 'lucide-react'
import { useBrowserToolbarConnection } from '../../hooks/useBrowserToolbarConnection'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'

/** The Browser entry shows the same connection the Browser panel reports. */
type Props = { workspacePath: string; profileId: string; onClick: () => void; status?: { connected: boolean; label: string; name?: string } }
export function WorkspaceBrowserRow(props: Props) {
  if (props.status) return <BrowserStatusRow status={props.status} onClick={props.onClick}/>
  return <BrowserStatusReader {...props}/>
}
function BrowserStatusReader({ workspacePath, profileId, onClick }: Props) {
  const status = useBrowserToolbarConnection(workspacePath, profileId)
  return <BrowserStatusRow status={status} onClick={onClick}/>
}
function BrowserStatusRow({ status, onClick }: { status: NonNullable<Props['status']>; onClick: () => void }) {
  return <Row icon={Monitor} hint={status.name} detail={status.name ? `${status.name} · ${status.label}` : status.label} onClick={onClick}>Browser</Row>
}
