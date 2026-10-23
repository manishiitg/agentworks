import { useCallback, useEffect, useRef, useState } from 'react'
import { KeyRound, Laptop, Plug } from 'lucide-react'
import { loadProjectVault, selectProjectVaultItem, type ProjectVaultInventory, type ProjectVaultItem } from '../../api/projectVault'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { Checkbox } from '../ui/checkbox'
import { useVaultSetupDraft } from './useVaultSetupDraft'

type Props = { workspacePath: string; tabId?: string; disabled?: boolean }

export function vaultItemDraft(item: ProjectVaultItem, vaultName: string): string {
  const name = item.name || item.provider || 'this app'
  if (item.kind === 'computer') return `Help me ${item.status === 'offline' ? 'reconnect' : 'use'} my computer “${name}” for this project. Check its connection and help me choose the folders and commands it can use.`
  if (item.kind === 'project_secret' || item.kind === 'shared_secret') return `Help me manage the secret “${name}” in “${vaultName}”. Ask what I want to change, store any value securely and never repeat it.`
  return `${item.status === 'needs_attention' ? 'Reconnect' : 'Help me manage'} ${item.provider || 'the app'} “${name}” in “${vaultName}”. Use the existing saved account and verify its status. Keep project selections unless I ask to change them.`
}

/** One metadata view; its only settings controls are project-use checkboxes. */
export function ProjectVaultPanel({ workspacePath, tabId, disabled = false }: Props) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${server}:${actor}:${workspacePath}`
  const [result, setResult] = useState<{ scope: string; inventory: ProjectVaultInventory }>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const generation = useRef(0)
  const setup = useVaultSetupDraft(tabId)
  const streaming = useChatStore(state => Boolean(tabId && state.chatTabs[tabId]?.isStreaming))
  const wasStreaming = useRef(false)
  const data = result?.scope === scope ? result.inventory : undefined
  const refresh = useCallback(async () => {
    const version = ++generation.current
    try {
      const inventory = await loadProjectVault(workspacePath)
      if (generation.current === version) { setResult({ scope, inventory }); setError('') }
    } catch { if (generation.current === version) setError('Your Vault could not be loaded. Ask in chat to check the connection.') }
  }, [scope, workspacePath])
  useEffect(() => {
    void refresh()
    const focus = () => { void refresh() }
    window.addEventListener('focus', focus)
    return () => { generation.current++; window.removeEventListener('focus', focus) }
  }, [refresh])
  useEffect(() => {
    if (wasStreaming.current && !streaming) void refresh()
    wasStreaming.current = streaming
  }, [streaming, refresh])
  useLiveRefetch(() => { void refresh() }, { kinds: ['plan'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0, minIntervalMs: 1000 })

  const toggle = async (item: ProjectVaultItem) => {
    if (!data || busy || disabled || !item.can_select) return
    const identity = scope
    generation.current++; setBusy(true); setError('')
    try {
      const inventory = await selectProjectVaultItem(workspacePath, item)
      if (identity === `${useWorkspaceConnectionStore.getState().activeWorkspaceId}:${useAuthStore.getState().user?.id}:${workspacePath}`) {
        generation.current++
        setResult({ scope, inventory })
        if (item.kind === 'computer') window.dispatchEvent(new Event('crew-laptop-settings-changed'))
      }
    } catch (cause) {
      const response = (cause as { response?: { data?: { error?: string } } })?.response?.data
      setError(response?.error || (cause instanceof Error ? cause.message : 'This selection could not be saved.'))
    } finally { setBusy(false) }
  }

  return <section aria-label="Project Vault" className="space-y-4 text-sm">
    <p className="text-xs text-muted-foreground">Tick what this project uses. Ask in chat to connect apps, add secrets or share a vault.</p>
    {(error || setup.error) && <p role="alert" className="text-xs text-destructive">{error || setup.error}</p>}
    {!data && !error && <p role="status" className="text-muted-foreground">Loading your vaults…</p>}
    {data?.notices.map(notice => <p key={notice} role="status" className="text-xs text-muted-foreground">{notice}</p>)}
    {data?.vaults.length === 0 && <p className="py-4 text-muted-foreground">No accounts or secrets yet. Ask this chat to connect your first app or create a vault.</p>}
    {data?.vaults.map(vault => <details key={`${scope}:${vault.id}`} open={!vault.project} className="overflow-hidden rounded-lg border border-border">
      <summary className="cursor-pointer px-3 py-2.5"><span className="font-medium">{vault.name}</span><span className="ml-2 text-xs text-muted-foreground">{vault.sharing}</span></summary>
      <div className="divide-y divide-border border-t border-border">
        {vault.items.map(item => {
          const secret = item.kind === 'project_secret' || item.kind === 'shared_secret'
          const Icon = secret ? KeyRound : item.kind === 'computer' ? Laptop : Plug
          const healthy = item.status === 'connected' || item.status === 'online'
          const status = item.kind === 'computer' ? healthy ? 'Online' : 'Offline' : healthy ? 'Connected' : 'Needs attention'
          return <div key={`${item.kind}:${item.id}`} className="flex min-h-12 items-center gap-3 px-3 py-2">
            <Checkbox aria-label={`Use ${item.name || item.provider} from ${vault.name}`} checked={item.selected} disabled={disabled || busy || !item.can_select} onCheckedChange={() => { void toggle(item) }} />
            <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <button type="button" disabled={disabled} onClick={() => { void setup.open(vaultItemDraft(item, vault.name)) }} className={`min-w-0 flex-1 truncate text-left hover:underline ${secret ? 'font-mono text-xs' : ''}`} title={item.name || item.provider}>{item.name || item.provider}</button>
            {!secret && <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground" title={status}><span aria-label={status} className={`h-2 w-2 rounded-full ${healthy ? 'bg-emerald-500' : 'bg-red-500'}`} />{item.kind === 'computer' && status}</span>}
          </div>
        })}
      </div>
    </details>)}
  </section>
}
