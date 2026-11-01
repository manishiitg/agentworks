import { Server } from 'lucide-react'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'

// Connected servers live on AI models & accounts → Servers (connect, open, disconnect there); there is no dialog.
let pendingServerAddress = ''

/** Opens the Servers page, with a server address to connect filled in when one is given. */
export function openConnectedServers(server = '', _connectionId?: string) {
  pendingServerAddress = server
  openWorkspaceSettings('providers')
  window.dispatchEvent(new CustomEvent('show-servers-page', { detail: { server } }))
}

/** The address handed to openConnectedServers, once. */
export function takePendingServerAddress(): string {
  const address = pendingServerAddress
  pendingServerAddress = ''
  return address
}

export function ConnectedServersMenuItem() {
  return hasDesktopConnections() ? (
    <button type="button" role="menuitem" data-product-navigation-action aria-label="Connected servers" onClick={() => openConnectedServers()}>
      <Server className="h-4 w-4" /><span>Connected servers</span>
    </button>
  ) : null
}
