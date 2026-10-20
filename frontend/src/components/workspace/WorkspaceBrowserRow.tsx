import { Monitor } from 'lucide-react'
import { useChromeExtensionConnection } from '../workflow/ChromeExtensionConnection'
import { WorkspaceContextRow as Row } from './WorkspaceContextCard'

/** The Browser entry shows the same connection the Browser panel reports. */
export function WorkspaceBrowserRow({ workspacePath, profileId, onClick }: { workspacePath: string; profileId: string; onClick: () => void }) {
  const { status, loading } = useChromeExtensionConnection(workspacePath, profileId)
  return <Row icon={Monitor} detail={loading ? undefined : status.connected ? 'Connected' : 'Not connected'} onClick={onClick}>Browser</Row>
}
