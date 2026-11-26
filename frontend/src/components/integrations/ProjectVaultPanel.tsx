import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Copy, Eye, EyeOff, KeyRound, Laptop, Plug } from 'lucide-react'
import { loadProjectVault, selectProjectVaultItem, setUseAllVaultApps, type ProjectVaultInventory, type ProjectVaultItem, type VaultItemKind } from '../../api/projectVault'
import { secretsApi } from '../../api/secrets'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { PROJECT_SECRETS_REFRESH_EVENT } from '../../utils/secretMutationRefresh'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { useVaultSetupDraft } from './useVaultSetupDraft'
import { AddToTeamControl, useAddToTeam } from '../../products/work/TeamWay'

type Props = { flat?: boolean; renderItemActions?: (item:ProjectVaultItem)=>ReactNode; workspacePath: string; tabId?: string; disabled?: boolean; itemKinds?: VaultItemKind[]; title?: string; onSelectionSaved?: (inventory: ProjectVaultInventory) => void }

export function vaultItemDraft(item: ProjectVaultItem, vaultName: string): string {
  const name = item.name || item.provider || 'this app'
  if (item.kind === 'computer') return `Help me ${item.status === 'offline' ? 'reconnect' : 'use'} my computer “${name}” for this project. Check its connection and help me choose the folders and commands it can use.`
  if (item.kind === 'shared_secret') return `Help me manage the secret “${name}” in “${vaultName}”. Ask what I want to change, store any value securely and never repeat it.`
  return `${item.status === 'needs_attention' ? 'Reconnect' : 'Help me manage'} ${item.provider || 'the app'} “${name}” in “${vaultName}”. Use the existing saved account and verify its status. Keep project selections unless I ask to change them.`
}

/** One metadata view; its only settings controls are project-use checkboxes. */
export function ProjectVaultPanel({ workspacePath, tabId, disabled = false, itemKinds, title, onSelectionSaved, flat=false, renderItemActions }: Props) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${server}:${actor}:${workspacePath}`
  const [result, setResult] = useState<{ scope: string; inventory: ProjectVaultInventory }>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [revealing, setRevealing] = useState<Record<string, boolean>>({})
  const [copied, setCopied] = useState('')
  const scopeRef = useRef(scope)
  scopeRef.current = scope
  const generation = useRef(0)
  const setup = useVaultSetupDraft(tabId)
  const projectId = useChatStore(state => tabId ? state.chatTabs[tabId]?.metadata?.agentProfileProjectId : undefined)
  const team = useAddToTeam(projectId)
  const streaming = useChatStore(state => Boolean(tabId && state.chatTabs[tabId]?.isStreaming))
  const wasStreaming = useRef(false)
  const data = result?.scope === scope ? result.inventory : undefined
  const secretsOnly = itemKinds?.every(kind => kind === 'shared_secret')
  // Forget revealed values whenever the active project, account or server changes.
  useEffect(() => { setRevealed({}); setRevealing({}); setCopied('') }, [scope])
  const groups = data?.vaults.map(vault => ({ ...vault, items: vault.items.filter(item => !itemKinds || itemKinds.includes(item.kind)) })).filter(vault => vault.items.length > 0)
  const refresh = useCallback(async () => {
    const version = ++generation.current
    try {
      const inventory = await loadProjectVault(workspacePath)
      if (generation.current === version) { setResult({ scope, inventory }); setError('') }
    } catch { if (generation.current === version) setError('Your integrations could not be loaded. Ask in chat to check the connection.') }
  }, [scope, workspacePath])
  useEffect(() => {
    void refresh()
    const focus = () => { void refresh() }
    window.addEventListener('focus', focus); window.addEventListener(PROJECT_SECRETS_REFRESH_EVENT, focus)
    return () => { generation.current++; window.removeEventListener('focus', focus); window.removeEventListener(PROJECT_SECRETS_REFRESH_EVENT, focus) }
  }, [refresh])
  useEffect(() => {
    if (wasStreaming.current && !streaming) void refresh()
    wasStreaming.current = streaming
  }, [streaming, refresh])
  useLiveRefetch(() => { void refresh() }, { kinds: ['plan'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0, minIntervalMs: 1000 })

  const toggle = async (item: ProjectVaultItem | 'all_vault_apps') => {
    if (!data || busy || disabled || (item !== 'all_vault_apps' && !item.can_select)) return
    const identity = scope
    generation.current++; setBusy(true); setError('')
    try {
      const inventory = item === 'all_vault_apps' ? await setUseAllVaultApps(workspacePath, !data.use_all_vault_apps) : await selectProjectVaultItem(workspacePath, item)
      if (identity === `${useWorkspaceConnectionStore.getState().activeWorkspaceId}:${useAuthStore.getState().user?.id}:${workspacePath}`) {
        generation.current++
        setResult({ scope, inventory })
        onSelectionSaved?.(inventory)
        if (item !== 'all_vault_apps' && item.kind === 'computer') window.dispatchEvent(new Event('crew-laptop-settings-changed'))
      }
    } catch (cause) {
      const response = (cause as { response?: { data?: { error?: string } } })?.response?.data
      setError(response?.error || (cause instanceof Error ? cause.message : 'This selection could not be saved.'))
    } finally { setBusy(false) }
  }

  const fetchSecretValue = async (id: string) => {
    const at = scopeRef.current
    const result = await secretsApi.revealGlobalSecret(id)
    return scopeRef.current === at ? result.value : null
  }
  const toggleReveal = async (item: ProjectVaultItem) => {
    if (revealed[item.id] !== undefined) {
      setRevealed(old => { const next = { ...old }; delete next[item.id]; return next })
      return
    }
    setError('')
    setRevealing(old => ({ ...old, [item.id]: true }))
    try {
      const value = await fetchSecretValue(item.id)
      if (value !== null) setRevealed(old => ({ ...old, [item.id]: value }))
    } catch (cause) {
      const response = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
      const msg = typeof response === 'string' ? response : response?.error
      setError(msg || (cause instanceof Error ? cause.message : 'Could not view this secret.'))
    } finally {
      setRevealing(old => { const next = { ...old }; delete next[item.id]; return next })
    }
  }
  // Copy asks the server each time so revoked secrets are never copied from memory.
  const copySecret = async (item: ProjectVaultItem) => {
    setCopied(''); setError('')
    try {
      const value = await fetchSecretValue(item.id)
      if (value === null) return
      await navigator.clipboard.writeText(value)
      setCopied(item.id)
    } catch {
      setRevealed(old => { const next = { ...old }; delete next[item.id]; return next })
      setError('Could not copy this secret.')
    }
  }

  const row = (item:ProjectVaultItem, groupName:string) => {
          const secret = item.kind === 'shared_secret'
          const Icon = secret ? KeyRound : item.kind === 'computer' ? Laptop : Plug
          const healthy = item.status === 'connected' || item.status === 'online'
          const status = item.kind === 'computer' ? healthy ? 'Online' : 'Offline' : healthy ? 'Connected' : 'Needs attention'
          const shownValue = secret ? revealed[item.id] : undefined
          return <div key={`${item.kind}:${item.id}`} data-item-kind={item.kind} data-item-id={item.id} className="flex min-h-12 flex-wrap items-center gap-3 px-3 py-2">
            <Checkbox aria-label={`Use ${item.name || item.provider} from ${groupName}`} checked={item.selected} disabled={disabled || busy || !item.can_select} onCheckedChange={() => { void toggle(item) }} />
            <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <button type="button" disabled={disabled} onClick={() => { void setup.open(vaultItemDraft(item, groupName)) }} className={`min-w-0 flex-1 truncate text-left hover:underline ${secret ? 'font-mono text-xs' : ''}`} title={item.name || item.provider}>{item.name || item.provider}</button>
            {(flat||item.source)&&<span className="text-xs text-muted-foreground">{flat?(item.source||groupName):`(${item.source})`}</span>}
            {secret && item.can_reveal && <div className="flex shrink-0 items-center gap-0.5">
              <Button type="button" size="xs" variant="ghost" disabled={Boolean(revealing[item.id])} aria-label={shownValue !== undefined ? `Hide ${item.name}` : `View ${item.name}`} title={shownValue !== undefined ? 'Hide secret value' : 'View secret value'} onClick={() => { void toggleReveal(item) }}>
                {shownValue !== undefined ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              </Button>
              <Button type="button" size="xs" variant="ghost" aria-label={copied === item.id ? `Copied ${item.name}` : `Copy ${item.name}`} title={copied === item.id ? 'Copied' : 'Copy secret value'} onClick={() => { void copySecret(item) }}>
                {copied === item.id ? <Check className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
              </Button>
            </div>}
            {renderItemActions?.(item)}
            {team && item.source === 'you' && (item.kind === 'mcp' || item.kind === 'shared_secret') && <AddToTeamControl team={team} kind={secret ? 'secret' : 'app'} id={secret ? item.name : item.id} provider={item.provider} signIn={item.sign_in} />}
            {!secret && <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground" title={status}><span aria-label={status} className={`h-2 w-2 rounded-full ${healthy ? 'bg-emerald-500' : 'bg-red-500'}`} />{status}</span>}
            {shownValue !== undefined && <div className="flex basis-full items-center gap-2 rounded-md border border-border bg-muted/40 px-2.5 py-1.5 pl-10">
              <code className="min-w-0 flex-1 break-all font-mono text-xs text-foreground">{shownValue}</code>
              <Button type="button" size="xs" variant="ghost" onClick={() => setRevealed(old => { const next = { ...old }; delete next[item.id]; return next })}>Hide</Button>
            </div>}
            {item.used_in && item.used_in.length > 0 && <span className="basis-full pl-10 text-xs text-muted-foreground">Used in: {item.used_in.join(', ')}</span>}
          </div>
  }
  const seen = new Set<string>()
  const flatRows = groups?.flatMap(group => group.items.map(item=>({item,groupName:group.name}))).filter(({item})=>{const key=`${item.kind}:${item.id}`;if(seen.has(key))return false;seen.add(key);return true})
  return <section aria-label={title || (secretsOnly ? "Shared secrets" : "Integrations")} className="space-y-4 text-sm">
    <p className="text-xs text-muted-foreground">{secretsOnly ? 'Choose the secrets this project can use. Their source is shown beside each name; use the eye icon to view or copy secrets you own.' : 'Tick what this project uses. Ask in chat to connect an app or add a secret.'}</p>
    {(error || setup.error) && <p role="alert" className="text-xs text-destructive">{error || setup.error}</p>}
    {!data && !error && <p role="status" className="text-muted-foreground">Loading integrations…</p>}
    {data && !secretsOnly && <label className="flex items-center gap-3 rounded-lg border border-border px-3 py-2.5">
      <Checkbox aria-label="Use all my apps automatically" checked={data.use_all_vault_apps} disabled={disabled || busy} onCheckedChange={() => { void toggle('all_vault_apps') }} />
      <span className="min-w-0 flex-1"><span className="block font-medium">Use all my apps automatically</span><span className="block text-xs text-muted-foreground">{data.use_all_vault_apps ? 'This project can use every Vault app you can use. Untick an app below to choose them yourself.' : 'This project uses only the apps ticked below.'}</span></span>
    </label>}
    {team?.line && <p role="status" className="text-xs text-muted-foreground">{team.line}</p>}
    {data?.notices.map(notice => <p key={notice} role="status" className="text-xs text-muted-foreground">{notice}</p>)}
    {groups?.length === 0 && <p className="py-4 text-muted-foreground">{secretsOnly ? 'No secrets are available to this project yet.' : 'No accounts or secrets yet. Ask this chat to connect your first app.'}</p>}
    {flat?<div className="divide-y divide-border rounded-lg border border-border" aria-label="Secret list">{flatRows?.map(({item,groupName})=>row(item,groupName))}</div>:groups?.map(vault => <details key={`${scope}:${vault.id}`} open className="overflow-hidden rounded-lg border border-border">
      <summary className="cursor-pointer px-3 py-2.5"><span className="font-medium">{vault.name}</span><span className="ml-2 text-xs text-muted-foreground">{vault.sharing}</span></summary>
      <div className="divide-y divide-border border-t border-border">
        {vault.items.map(item=>row(item,vault.name))}
      </div>
    </details>)}
  </section>
}
