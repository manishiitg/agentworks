import { useCallback, useEffect, useRef, useState } from 'react'
import { KeyRound, Users, RefreshCw } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { McpConnectionCard } from './McpConnectionsPanel'
import { OpenVaultButton } from './OpenVaultButton'
import type { VaultMcpServer } from './useVaultMcpConnections'

type Secret = { name: string }
type Group = { id: string; name: string; description: string; servers: VaultMcpServer[]; secrets: Secret[] }
type Inventory = { groups: Group[]; servers: VaultMcpServer[]; secrets: Secret[] }

/** Caller-authorized metadata only; project selection never grants Vault permission. */
export function ProjectVaultPanel({ selectedServers, onSelectedServersChange, selectedSecrets, onSelectedSecretsChange, disabled = false }: {
  selectedServers: string[]; onSelectedServersChange: (names: string[]) => Promise<unknown> | void
  selectedSecrets: string[]; onSelectedSecretsChange: (names: string[]) => Promise<unknown> | void; disabled?: boolean
}) {
  const [inventory, setInventory] = useState<Inventory>({ groups: [], servers: [], secrets: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const generation = useRef(0)
  const refresh = useCallback(async (background = false) => {
    const version = ++generation.current
    if (!background) setLoading(true)
    try {
      const response = await api.get<Inventory>('/api/me/mcp/vault')
      if (version !== generation.current) return
      setInventory({ groups: response.data.groups ?? [], servers: response.data.servers ?? [], secrets: response.data.secrets ?? [] })
      setError('')
    } catch {
      if (version === generation.current) { setInventory({ groups: [], servers: [], secrets: [] }); setError('Could not load your Vault access.') }
    } finally { if (version === generation.current) setLoading(false) }
  }, [])
  useEffect(() => {
    void refresh()
    const onFocus = () => { void refresh(true) }
    window.addEventListener('focus', onFocus)
    return () => { generation.current++; window.removeEventListener('focus', onFocus) }
  }, [refresh])
  const change = async (name: string, kind: 'server' | 'secret') => {
    setBusy(true); setError('')
    const selected = kind === 'server' ? selectedServers.filter(item => item !== 'NO_SERVERS') : selectedSecrets
    const next = selected.includes(name) ? selected.filter(item => item !== name) : [...selected, name]
    try { await (kind === 'server' ? onSelectedServersChange(next) : onSelectedSecretsChange(next)) }
    catch { setError('Could not save Vault selection.') }
    finally { setBusy(false) }
  }
  const groupedServers = new Set(inventory.groups.flatMap(group => group.servers.map(server => server.id)))
  const groupedSecrets = new Set(inventory.groups.flatMap(group => group.secrets.map(secret => secret.name)))
  const extraServers = inventory.servers.filter(server => !groupedServers.has(server.id))
  const extraSecrets = inventory.secrets.filter(secret => !groupedSecrets.has(secret.name))
  const groups = [...inventory.groups, ...(extraServers.length || extraSecrets.length ? [{ id: 'other-access', name: 'Other access', description: '', servers: extraServers, secrets: extraSecrets }] : [])]
  const availableServers = new Set(inventory.servers.map(server => `vault_${server.id}`))
  const availableSecrets = new Set(inventory.secrets.map(secret => secret.name))
  const missingServers = selectedServers.filter(name => name.startsWith('vault_') && !availableServers.has(name))
  const missingSecrets = selectedSecrets.filter(name => !availableSecrets.has(name))
  return <div className="space-y-3 text-xs">
    <div className="flex items-center justify-between gap-2">
      <span className="text-muted-foreground">Your groups · select resources for this project</span>
      <div className="flex items-center gap-1"><OpenVaultButton panel="servers"/><Button size="icon" variant="ghost" className="h-7 w-7" aria-label="Refresh Vault access" disabled={loading} onClick={() => void refresh()}><RefreshCw className={loading ? 'animate-spin' : ''}/></Button></div>
    </div>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {loading ? <p className="text-muted-foreground">Loading your access…</p> : <>
      {groups.length === 0 && <p className="py-4 text-muted-foreground">No Vault groups or shared resources available.</p>}
      {groups.map(group => <details key={group.id} open className="rounded-md border border-border" aria-label={group.name}>
        <summary className="flex cursor-pointer items-center gap-2 p-3"><Users className="h-3.5 w-3.5 text-primary"/><span className="font-medium">{group.name}</span><span className="ml-auto text-muted-foreground">{group.servers.length} {group.servers.length === 1 ? 'connection' : 'connections'} · {group.secrets.length} {group.secrets.length === 1 ? 'secret' : 'secrets'}</span></summary>
        <div className="space-y-2 border-t border-border p-3">
          {group.description && <p className="text-muted-foreground">{group.description}</p>}
          {group.servers.map(server => <McpConnectionCard key={server.id} showTools={false} server={{ id: `vault_${server.id}`, name: server.label, source: 'Vault', status: 'Shared connection', selection: { label: `Use ${server.label} from ${group.name}`, checked: selectedServers.includes(`vault_${server.id}`), disabled: disabled || busy, change: () => change(`vault_${server.id}`, 'server') } }}/>) }
          {group.secrets.map(secret => <label key={secret.name} className="flex items-center gap-2 rounded-md border border-border px-3 py-2"><Checkbox aria-label={`Use ${secret.name} from ${group.name}`} checked={selectedSecrets.includes(secret.name)} disabled={disabled || busy} onCheckedChange={() => void change(secret.name, 'secret')}/><KeyRound className="h-3.5 w-3.5 text-primary"/><span className="font-mono">{secret.name}</span></label>)}
          {!group.servers.length && !group.secrets.length && <p className="text-muted-foreground">No resources assigned.</p>}
        </div>
      </details>)}
      {(missingServers.length > 0 || missingSecrets.length > 0) && <div className="space-y-1 border-t border-border pt-3"><p className="text-muted-foreground">No longer available</p>{[...missingServers.map(name => ({ name, kind: 'server' as const })), ...missingSecrets.map(name => ({ name, kind: 'secret' as const }))].map(item => <div key={item.name} className="flex items-center justify-between gap-2"><span>{item.name}</span><Button variant="ghost" size="xs" disabled={disabled || busy} onClick={() => void change(item.name, item.kind)}>Remove selection</Button></div>)}</div>}
    </>}
  </div>
}
