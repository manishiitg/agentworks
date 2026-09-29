import { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, Plus, Trash2, UserRound } from 'lucide-react'
import ConnectionIcon from '../../components/connectors/ConnectionIcon'
import { brandSlugFor } from '../../components/connectors/brandSlug'
import { Button } from '../../components/ui/Button'
import ConfirmationDialog from '../../components/ui/ConfirmationDialog'
import { personalMcpApi, type PersonalMcpCatalogServer } from '../../api/personalMcp'
import { placeMcpApi, type PlaceMcpServer } from '../../api/placeMcp'

const errorText = (cause: unknown, fallback: string) => {
  const response = (cause as { response?: { data?: { error?: string } } })?.response
  return response?.data?.error || (cause instanceof Error ? cause.message : fallback)
}

/**
 * Connections with a person's own login in a workflow or Crew
 * (docs/design/personal_mcp_attach.md). Someone who can edit it adds, say,
 * their Gmail; every chat and run there then uses it like any other MCP
 * server. It belongs to this place only, never their Code.
 */
export function PlaceMcpSection({ workspacePath, placeNoun, canEdit }: {
  workspacePath: string
  /** "Crew" or "workflow", for the warning. */
  placeNoun: string
  /** The viewer can add connections here (edit access). */
  canEdit: boolean
}) {
  const [servers, setServers] = useState<PlaceMcpServer[]>([])
  const [catalog, setCatalog] = useState<PersonalMcpCatalogServer[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [picking, setPicking] = useState(false)
  const [confirmAdd, setConfirmAdd] = useState<PersonalMcpCatalogServer | null>(null)

  const refresh = useCallback(async () => {
    try {
      setServers(await placeMcpApi.list(workspacePath))
      setError(null)
    } catch (cause) {
      setError(errorText(cause, 'Could not load connections.'))
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => {
    if (!picking || catalog.length > 0) return
    void personalMcpApi.catalog().then(list => setCatalog(list.filter(entry => entry.sign_in))).catch(() => setCatalog([]))
  }, [picking, catalog.length])
  // A sign-in finishes in another tab; pick up its result when the person
  // comes back.
  useEffect(() => {
    const onFocus = () => { void refresh() }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [refresh])

  const mineByCatalog = useMemo(() => new Set(servers.filter(s => s.mine).map(s => s.catalog || s.name)), [servers])

  const connect = async (name: string) => {
    setBusy(name)
    setMessage(null)
    try {
      const result = await placeMcpApi.connect(workspacePath, name)
      if (result.auth_url) window.open(result.auth_url, '_blank', 'noopener')
      else if (result.message) setMessage(result.message)
    } catch (cause) {
      setError(errorText(cause, 'Could not start sign-in.'))
    } finally {
      setBusy(null)
    }
  }

  const add = async (entry: PersonalMcpCatalogServer) => {
    setBusy(entry.catalog)
    setError(null)
    try {
      const saved = await placeMcpApi.add(workspacePath, entry.catalog)
      setPicking(false)
      await refresh()
      if (saved.oauth) await connect(saved.name)
    } catch (cause) {
      setError(errorText(cause, 'Could not add the connection.'))
    } finally {
      setBusy(null)
    }
  }

  const remove = async (server: PlaceMcpServer) => {
    setBusy(`${server.owner}:${server.name}`)
    try {
      await placeMcpApi.remove(workspacePath, server.name, server.owner)
      await refresh()
    } catch (cause) {
      setError(errorText(cause, 'Could not remove the connection.'))
    } finally {
      setBusy(null)
    }
  }

  if (loading) return null
  if (servers.length === 0 && !canEdit) return null

  return (
    <div data-testid="place-mcp-section" className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <div className="text-sm font-medium text-muted-foreground">Connected with a person's login</div>
        {canEdit && (
          <Button size="sm" variant="outline" onClick={() => setPicking(open => !open)}>
            <Plus className="mr-1 h-3.5 w-3.5" />Add with your login
          </Button>
        )}
      </div>
      <p className="text-xs leading-5 text-muted-foreground">
        Everyone who uses this {placeNoun} uses these with the login of the person who added them. They stay in this {placeNoun} only.
      </p>
      {error && <p className="text-xs text-destructive">{error}</p>}
      {message && <p className="text-xs text-amber-700 dark:text-amber-300">{message}</p>}
      {servers.map(server => (
        <div key={`${server.owner}:${server.name}`} className={`flex items-center gap-2 rounded-md border border-border px-3 py-2 text-sm ${server.active ? '' : 'opacity-60'}`}>
          <ConnectionIcon icon={brandSlugFor(server.catalog || server.name)} name={server.catalog || server.name} size="xs" />
          <div className="min-w-0 flex-1">
            <div className="truncate font-medium">{server.catalog || server.name}</div>
            <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
              <UserRound className="h-3 w-3" />
              {server.mine ? 'your login' : `${server.owner_name}'s login`}
              {!server.active && ' · paused: they can no longer edit here'}
              {server.active && !server.connected && ' · not signed in yet'}
            </div>
          </div>
          {server.mine && !server.connected && (
            <Button size="sm" disabled={busy !== null} onClick={() => { void connect(server.name) }}>
              {busy === server.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : 'Sign in'}
            </Button>
          )}
          {(server.mine || canEdit) && (
            <Button size="icon" variant="ghost" className="h-7 w-7" title="Remove from this place (their login is deleted)" disabled={busy !== null} onClick={() => { void remove(server) }}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          )}
        </div>
      ))}
      {picking && (
        <div className="grid grid-cols-1 gap-1.5 rounded-md border border-dashed border-border p-2 sm:grid-cols-2">
          {catalog.length === 0 && <span className="text-xs text-muted-foreground">Loading…</span>}
          {catalog.filter(entry => !mineByCatalog.has(entry.catalog)).map(entry => (
            <button
              key={entry.catalog}
              type="button"
              disabled={busy !== null}
              onClick={() => setConfirmAdd(entry)}
              className="flex items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted"
            >
              <ConnectionIcon icon={brandSlugFor(entry.catalog)} name={entry.catalog} size="xs" />
              <span className="truncate">{entry.catalog}</span>
              {busy === entry.catalog && <Loader2 className="ml-auto h-3.5 w-3.5 animate-spin" />}
            </button>
          ))}
        </div>
      )}
      <ConfirmationDialog
        isOpen={confirmAdd !== null}
        onClose={() => setConfirmAdd(null)}
        onConfirm={() => { const entry = confirmAdd; setConfirmAdd(null); if (entry) void add(entry) }}
        title={`Add ${confirmAdd?.catalog ?? ''} with your login?`}
        message={`Everyone who can use this ${placeNoun} — every chat, schedule, trigger, workflow that calls it and Slack channel it answers in — can use ${confirmAdd?.catalog ?? 'it'} as you. It stays in this ${placeNoun} only. You can remove it any time.`}
        confirmText="Add with my login"
        type="warning"
      />
    </div>
  )
}
