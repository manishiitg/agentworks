import { useEffect, useState } from 'react'
import api from '../../services/api'
import { CliMcpSetupPanel } from '../../components/integrations/CliMcpSetupPanel'

/** Vault uses the shared MCP client setup UI with its own endpoint and OAuth grants. */
export function GatewayConnectPanel({ base: _base }: { base: string }) {
  const [endpoint, setEndpoint] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    void api.get<{ endpoint: string }>('/api/vault/connection')
      .then(({ data }) => {
        if (!active) return
        if (typeof data.endpoint === 'string' && data.endpoint) setEndpoint(data.endpoint)
        else setError('Vault’s MCP endpoint is not configured.')
      })
      .catch(() => { if (active) setError('Could not load the Vault connection. Check the server configuration.') })
    return () => { active = false }
  }, [])
  return <div data-testid="gateway-connect">
    {error ? <p role="alert" className="text-sm text-destructive">{error}</p>
      : endpoint ? <CliMcpSetupPanel target="vault" endpoint={endpoint} />
        : <p role="status" className="text-sm text-muted-foreground">Loading connection…</p>}
  </div>
}
