import { useEffect, useState } from 'react'
import { McpConnectionsPanel } from './McpConnectionsPanel'
import { useVaultMcpConnections } from './useVaultMcpConnections'
import { useVaultSetupDraft } from './useVaultSetupDraft'
import { vaultConnectionsApi, type VaultCatalogApp } from '../../api/vaultConnections'

/** Account inventory only: no Crew selection, using the shared Vault MCP layout and API. */
export default function AccountMcpPanel() {
  const vault = useVaultMcpConnections()
  const setup = useVaultSetupDraft()
  const [catalog, setCatalog] = useState<VaultCatalogApp[]>([])
  const [catalogError, setCatalogError] = useState('')
  useEffect(() => {
    let active = true
    void vaultConnectionsApi.catalog().then(items => { if (active) setCatalog(items) })
      .catch(() => { if (active) setCatalogError('Could not load the connection catalog. Refresh to try again.') })
    return () => { active = false }
  }, [])
  return <McpConnectionsPanel
    servers={vault.servers.map(server => ({ ...server, actions: [{
      label: 'Manage in chat',
      run: () => setup.open(`Manage the saved Vault MCP connection ${server.id}. Inspect this exact connection and guide setup in chat. Preserve existing accounts, grants and Crew selections.`),
    }] }))}
    catalog={catalog.map(app => ({ id: app.name, name: app.name,
      description: app.oauth ? 'Sign in with your account' : 'Connect an account',
      connect: { label: 'Connect in chat', run: () => setup.open(`Connect ${app.name} in Vault. Guide credential or OAuth setup, return the real sign-in link and verify discovery. Preserve existing accounts and permissions; do not change any Crew selections.`) },
    }))}
    loading={vault.loading} refresh={() => { void vault.refresh() }}
    notices={[...vault.notices, ...(catalogError ? [{ message: catalogError }] : []), ...(setup.error ? [{ message: setup.error }] : [])]}
    description="Your saved MCP connections. Open a connection to see its tools. Choose what each Crew uses in that Crew’s settings."
    addCustom={{ label: 'Add custom server', run: () => setup.open('Help me connect a custom MCP server in Vault. Ask for its name and URL, guide setup in chat and verify discovery. Preserve existing accounts, grants and Crew selections.') }}
  />
}
