import { OpenVaultButton } from '../integrations/OpenVaultButton'
import { useCallback, useEffect, useRef, useState } from 'react'
import axios from 'axios'
import {
  KeyRound,
  Plus,
  Eye,
  EyeOff,
  Trash2,
  RefreshCw,
  Copy,
  Check,
  RotateCw,
  ChevronDown,
  Share2,
} from 'lucide-react'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { Input } from '../ui/Input'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { secretsApi } from '../../api/secrets'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { PROJECT_SECRETS_REFRESH_EVENT } from '../../utils/secretMutationRefresh'

interface SecretSelectionSectionProps {
  selectedSecrets: string[]
  onSecretChange: (names: string[]) => void | Promise<unknown>
  selectedGlobalSecrets?: string[] | null
  onGlobalSecretChange?: (names: string[] | null) => void | Promise<unknown>
  workflowPath?: string
  fillAvailableHeight?: boolean
  workspaceNoun?: string
  workspaceSecretHeading?: string
  showGlobalSecrets?: boolean
  workspaceSecretsAlwaysEnabled?: boolean
  allowGlobalPromotion?: boolean
  persistExplicitGlobalSelection?: boolean
  /** The same list and secure editor serve project selection and Vault management. */
  mode?: 'project' | 'vault' | 'group'
  groupId?: string
  groupSelectedNames?: string[]
  onGroupAccessChange?: (name: string, allowed: boolean) => Promise<unknown>
}
type Row = { name: string; managed?: boolean; encrypted_value?: string }
function message(error: unknown) {
  return axios.isAxiosError(error) &&
    typeof error.response?.data?.error === 'string'
    ? error.response.data.error
    : axios.isAxiosError(error) && typeof error.response?.data === 'string'
      ? error.response.data
      : error instanceof Error
        ? error.message
        : 'Could not update secrets.'
}
export function SecretSelectionSection({
  selectedSecrets,
  onSecretChange,
  selectedGlobalSecrets = [],
  onGlobalSecretChange,
  workflowPath = '',
  fillAvailableHeight,
  showGlobalSecrets = true,
  workspaceSecretsAlwaysEnabled = false,
  allowGlobalPromotion = true,
  mode = 'project',
  groupSelectedNames = [],
  onGroupAccessChange,
}: SecretSelectionSectionProps) {
  const [shareGroups, setShareGroups] = useState<{ ID: string; Name: string; Description?: string }[]>([])
  const [sharing, setSharing] = useState<Row | null>(null)
  const [shareName, setShareName] = useState('')
  const [shareGroupIds, setShareGroupIds] = useState<string[]>([])
  const [shareNotice, setShareNotice] = useState('')
  const [source, setSource] = useState<'project' | 'vault'>('project')
  const loadGeneration = useRef(0)
  const viewGeneration = useRef(0)
  useEffect(() => {
    viewGeneration.current += 1
    return () => {
      viewGeneration.current += 1
    }
  }, [workflowPath, mode, source])
  const canWrite = useCanWriteWorkflow(workflowPath || undefined)
  useEffect(() => {
    let cancelled = false
    setShareGroups([])
    setSharing(null)
    setShareNotice('')
    if (mode === 'project' && workflowPath && canWrite && allowGlobalPromotion) {
      void secretsApi.getVaultShareGroups().then(({ groups }) => {
        if (!cancelled) setShareGroups(groups)
      }).catch(() => { /* The server exposes sharing only to Vault administrators. */ })
    }
    return () => { cancelled = true }
  }, [workflowPath, mode, canWrite, allowGlobalPromotion])
  const [project, setProject] = useState<Row[]>([])
  const [vault, setVault] = useState<Row[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [editor, setEditor] = useState(false)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [copied, setCopied] = useState('')
  const [showAvailable, setShowAvailable] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<Row | null>(null)
  const isVault = mode !== 'project' || source === 'vault'
  const canManage =
    mode === 'vault' || (mode === 'project' && !isVault && canWrite)
  const load = useCallback(async () => {
    const request = ++loadGeneration.current
    setLoading(true)
    setError('')
    try {
      const [p, v] = await Promise.allSettled([
        mode === 'project' && workflowPath
          ? secretsApi.listWorkflowSecrets(workflowPath)
          : Promise.resolve([]),
        showGlobalSecrets || mode !== 'project'
          ? secretsApi.getGlobalSecrets(mode !== 'project')
          : Promise.resolve([]),
      ])
      if (request !== loadGeneration.current) return
      setProject(p.status === 'fulfilled' ? p.value : [])
      setVault(v.status === 'fulfilled' ? v.value : [])
      if (p.status === 'rejected') setError(message(p.reason))
      else if (v.status === 'rejected') setError(message(v.reason))
    } catch (e) {
      if (request !== loadGeneration.current) return
      setProject([])
      setVault([])
      setError(message(e))
    } finally {
      if (request === loadGeneration.current) setLoading(false)
    }
  }, [workflowPath, mode, showGlobalSecrets])
  useEffect(() => {
    setRevealed({})
    setCopied('')
    setBusy('')
    setValue('')
    setEditor(false)
    void load()
    return () => {
      loadGeneration.current += 1
    }
  }, [load])
  useEffect(() => {
    const refresh = () => {
      setRevealed({})
      void load()
    }
    window.addEventListener(PROJECT_SECRETS_REFRESH_EVENT, refresh)
    return () =>
      window.removeEventListener(PROJECT_SECRETS_REFRESH_EVENT, refresh)
  }, [load])
  useEffect(() => {
    setRevealed({})
    setValue('')
    setEditor(false)
    setCopied('')
    setBusy('')
    setSharing(null)
    setShareNotice('')
  }, [source])
  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(''), 2000)
    return () => clearTimeout(timer)
  }, [copied])
  const rows = isVault ? vault : project
  const selected =
    mode === 'group'
      ? groupSelectedNames
      : isVault
        ? (selectedGlobalSecrets ?? [])
        : selectedSecrets
  const missing = selected.filter((n) => !rows.some((s) => s.name === n))
  const toggle = async (row: Row) => {
    setBusy(row.name)
    setError('')
    try {
      const next = selected.includes(row.name)
        ? selected.filter((n) => n !== row.name)
        : [...selected, row.name]
      if (mode === 'group')
        await onGroupAccessChange?.(row.name, !selected.includes(row.name))
      else if (isVault) await onGlobalSecretChange?.(next)
      else await onSecretChange(next)
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy('')
    }
  }
  const save = async () => {
    const n = name.trim().toUpperCase()
    if (!/^[A-Z_][A-Z0-9_]{0,127}$/.test(n) || !value) {
      setError('Enter a valid name and a value.')
      return
    }
    setBusy(n)
    setError('')
    try {
      if (mode === 'vault') await secretsApi.saveGlobalSecret(n, value)
      else {
        const { encrypted } = await secretsApi.encrypt(value)
        await secretsApi.storeWorkflowSecret(workflowPath, n, encrypted)
        if (!selectedSecrets.includes(n))
          await onSecretChange([...selectedSecrets, n])
      }
      setValue('')
      setName('')
      setEditor(false)
      setRevealed({})
      await load()
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy('')
    }
  }
  const readValue = (row: Row) =>
    mode === 'vault'
      ? secretsApi.revealGlobalSecret(row.name)
      : secretsApi.decrypt(row.encrypted_value!, workflowPath)
  const copy = async (row: Row) => {
    const view = viewGeneration.current
    setBusy(row.name)
    setCopied('')
    setError('')
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error(
          'Clipboard is unavailable. Use Reveal to view the value.',
        )
      }
      // Fetch on every copy so a previously revealed value cannot bypass current access checks.
      const result = await readValue(row)
      if (view !== viewGeneration.current) return
      await navigator.clipboard.writeText(result.value)
      if (view === viewGeneration.current) setCopied(row.name)
    } catch {
      if (view === viewGeneration.current)
        setError(
          'Could not copy the secret. Check your access and clipboard permission.',
        )
    } finally {
      if (view === viewGeneration.current) setBusy('')
    }
  }
  const reveal = async (row: Row) => {
    const view = viewGeneration.current
    if (revealed[row.name] !== undefined) {
      setRevealed((current) => {
        const next = { ...current }
        delete next[row.name]
        return next
      })
      return
    }
    setBusy(row.name)
    setError('')
    try {
      const result = await readValue(row)
      if (view === viewGeneration.current)
        setRevealed((current) => ({ ...current, [row.name]: result.value }))
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy('')
    }
  }
  const remove = async () => {
    if (!pendingDelete) return
    setBusy(pendingDelete.name)
    setError('')
    try {
      if (mode === 'vault')
        await secretsApi.deleteGlobalSecret(pendingDelete.name)
      else {
        await secretsApi.deleteWorkflowSecret(workflowPath, pendingDelete.name)
        await onSecretChange(
          selectedSecrets.filter((n) => n !== pendingDelete.name),
        )
      }
      setRevealed({})
      setPendingDelete(null)
      await load()
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy('')
    }
  }
  const share = async () => {
    if (!sharing) return
    const view = viewGeneration.current
    setBusy(sharing.name)
    setError('')
    setShareNotice('')
    try {
      await secretsApi.shareWorkflowSecret(workflowPath, sharing.name, shareName.trim(), shareGroupIds)
      if (view !== viewGeneration.current) return
      setSharing(null)
      setShareNotice(`Shared as ${shareName.trim()}. The project copy is unchanged.`)
      window.dispatchEvent(new Event(PROJECT_SECRETS_REFRESH_EVENT))
    } catch (e) {
      if (view === viewGeneration.current) setError(message(e))
    } finally {
      if (view === viewGeneration.current) setBusy('')
    }
  }
  const renderShareForm = () => sharing ? (
        <form
          aria-label={`Share ${sharing.name} to Vault`}
          className="w-full space-y-3 rounded-lg border border-primary/30 bg-primary/5 p-3"
          onSubmit={(e) => { e.preventDefault(); void share() }}
        >
          <h4 className="text-sm font-medium">Share {sharing.name} to Vault</h4>
          <p className="text-xs text-muted-foreground">Keeps the project copy. Copies rotate independently.</p>
          <label className="block space-y-1 text-xs">
            <span>Vault secret name</span>
            <Input aria-label="Vault secret name" value={shareName} disabled={!!busy} onChange={(e) => setShareName(e.target.value)} />
          </label>
          <fieldset disabled={!!busy} className="space-y-2">
            <legend className="mb-2 text-xs font-medium">Give access to</legend>
            {shareGroups.map((g) => (
              <label key={g.ID} className="flex cursor-pointer items-start gap-2 rounded-md border border-border bg-background p-2 text-xs">
                <Checkbox aria-label={`Share with ${g.Name}`} checked={shareGroupIds.includes(g.ID)} onCheckedChange={(checked) => setShareGroupIds((ids) => checked === true ? [...ids, g.ID] : ids.filter((id) => id !== g.ID))} />
                <span><span className="font-medium">{g.Name}</span>{g.Description && <span className="mt-0.5 block text-muted-foreground">{g.Description}</span>}</span>
              </label>
            ))}
          </fieldset>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" size="sm" disabled={!!busy} onClick={() => setSharing(null)}>Cancel</Button>
            <Button type="submit" size="sm" disabled={!!busy || !/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(shareName.trim()) || !shareGroupIds.length}>{busy ? 'Sharing…' : 'Share to Vault'}</Button>
          </div>
        </form>
  ) : null
  const renderRow = (row: Row) => (
    <div
      key={row.name}
      className="flex flex-wrap items-center gap-x-2 gap-y-2 rounded-md border border-border bg-muted/10 px-3 py-2"
    >
      <div className="flex min-w-0 flex-1 basis-40 items-center gap-2">
        {mode !== 'vault' && (
          <Checkbox
            className="h-3.5 w-3.5 [&_svg]:h-3 [&_svg]:w-3"
            aria-label={`${mode === 'group' ? 'Allow' : 'Use'} ${row.name}`}
            checked={
              (workspaceSecretsAlwaysEnabled && !isVault) ||
              selected.includes(row.name)
            }
            disabled={
              !!busy ||
              (workspaceSecretsAlwaysEnabled && !isVault) ||
              (mode === 'project' &&
                (!canWrite || (isVault && !onGlobalSecretChange)))
            }
            onCheckedChange={() => void toggle(row)}
          />
        )}
        <div
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary"
          aria-hidden="true"
        >
          <KeyRound className="h-3.5 w-3.5" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="break-all font-mono text-xs font-medium">{row.name}</p>
          {canManage &&
            (mode === 'vault' || row.encrypted_value) &&
            revealed[row.name] === undefined && (
              <p
                aria-label="Value hidden"
                className="mt-0.5 font-mono text-[10px] tracking-widest text-muted-foreground"
              >
                <span aria-hidden="true">••••••••</span>
              </p>
            )}
          {mode === 'vault' && !row.managed && (
            <p className="text-xs text-muted-foreground">
              Configured in server environment
            </p>
          )}
        </div>
      </div>
      {canManage && (
        <div
          role="group"
          aria-label={`Actions for ${row.name}`}
          className="ml-auto flex shrink-0 items-center gap-0.5 rounded-md bg-background/60 p-0.5"
        >
          {(mode === 'vault' || row.encrypted_value) && (
            <>
              <Button
                size="icon"
                variant="ghost"
                className="h-8 w-8 [&_svg]:h-3.5 [&_svg]:w-3.5 text-muted-foreground hover:text-foreground"
                aria-label={`${copied === row.name ? 'Copied' : 'Copy'} ${row.name}`}
                title={copied === row.name ? 'Copied' : 'Copy secret value'}
                disabled={!!busy}
                onClick={() => void copy(row)}
              >
                {copied === row.name ? (
                  <Check className="h-3.5 w-3.5 text-primary" />
                ) : (
                  <Copy className="h-3.5 w-3.5" />
                )}
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="h-8 w-8 [&_svg]:h-3.5 [&_svg]:w-3.5 text-muted-foreground hover:text-foreground"
                aria-label={`${revealed[row.name] !== undefined ? 'Hide' : 'Reveal'} ${row.name}`}
                title={
                  revealed[row.name] !== undefined
                    ? 'Hide secret value'
                    : 'Reveal secret value'
                }
                disabled={!!busy}
                onClick={() => void reveal(row)}
              >
                {revealed[row.name] !== undefined ? (
                  <EyeOff className="h-3.5 w-3.5" />
                ) : (
                  <Eye className="h-3.5 w-3.5" />
                )}
              </Button>
            </>
          )}
          {mode === 'project' && !isVault && shareGroups.length > 0 && row.encrypted_value && (
            <Button
              size="icon"
              variant="ghost"
              className="h-8 w-8 [&_svg]:h-3.5 [&_svg]:w-3.5 text-muted-foreground hover:text-foreground"
              aria-label={`Share ${row.name} to Vault`}
              title="Share to Vault"
              disabled={!!busy}
              onClick={() => {
                setSharing(row)
                setShareName(row.name)
                setShareGroupIds([])
                setShareNotice('')
                setError('')
                setEditor(false)
              }}
            >
              <Share2 />
            </Button>
          )}
          {(mode !== 'vault' || row.managed) && (
            <>
              <Button
                size="icon"
                variant="ghost"
                className="h-8 w-8 [&_svg]:h-3.5 [&_svg]:w-3.5 text-muted-foreground hover:text-foreground"
                aria-label={`Rotate ${row.name}`}
                title="Rotate saved value"
                disabled={!!busy}
                onClick={() => {
                  setName(row.name)
                  setValue('')
                  setEditor(true)
                }}
              >
                <RotateCw className="h-3.5 w-3.5" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="h-8 w-8 [&_svg]:h-3.5 [&_svg]:w-3.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                aria-label={`Delete ${row.name}`}
                title="Delete secret"
                disabled={!!busy}
                onClick={() => setPendingDelete(row)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </>
          )}
        </div>
      )}
      {sharing?.name === row.name && !isVault && renderShareForm()}
      {revealed[row.name] !== undefined && (
        <p className="w-full whitespace-pre-wrap break-all rounded-md border border-border bg-background px-3 py-2 font-mono text-xs">
          {revealed[row.name]}
        </p>
      )}
    </div>
  )
  return (
    <section
      aria-label={
        mode === 'group'
          ? 'Group secrets'
          : mode === 'vault'
            ? 'Vault secrets'
            : 'Project integrations secrets'
      }
      className={
        fillAvailableHeight ? 'flex h-full min-h-0 flex-col gap-3' : 'space-y-3'
      }
    >
      <div className="flex items-center justify-between gap-2">
        {mode === 'project' && showGlobalSecrets ? (
          <div role="tablist" aria-label="Secret source" className="flex gap-1">
            {(['project', 'vault'] as const).map((s) => (
              <Button
                key={s}
                role="tab"
                aria-selected={source === s}
                size="sm"
                variant={source === s ? 'secondary' : 'ghost'}
                onClick={() => setSource(s)}
              >
                {s === 'project' ? 'Project' : 'Vault'}
              </Button>
            ))}
          </div>
        ) : (
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <KeyRound className="h-4 w-4 text-primary" />
            Secrets
            {mode === 'group' && (
              <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-normal text-muted-foreground">
                {selected.length} assigned
              </span>
            )}
          </h3>
        )}
        <div className="flex gap-1">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Refresh secrets"
            disabled={loading || !!busy}
            onClick={() => void load()}
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
        </div>
      </div>
      {mode === 'project' && isVault && (
        <div className="flex justify-end">
          <OpenVaultButton />
        </div>
      )}
      {mode !== 'group' && (
        <div
          role="note"
          className="space-y-2 rounded-md border border-primary/20 bg-primary/5 px-3 py-2 text-xs leading-relaxed text-muted-foreground"
        >
          {isVault && (
            <div>
              <p className="font-medium text-foreground">Platform secrets</p>
              <p>
                {mode === 'project'
                  ? 'Shared secrets your groups allow you to use in this project.'
                  : 'Shared across products. Assign access in Access → Groups → Permissions.'}
              </p>
            </div>
          )}
          <details>
            <summary className="cursor-pointer font-medium text-foreground">
              Removing access
            </summary>
            <p className="mt-1">
              Removing access won’t disable a secret the user already has.
              Disable the old secret in the original service, then save a new
              value {isVault ? 'in Vault' : 'here'}.
            </p>
          </details>
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {shareNotice && <p role="status" className="text-xs text-primary">{shareNotice}</p>}

      {loading && !rows.length ? (
        <p className="text-sm text-muted-foreground">Loading secrets…</p>
      ) : (
        <div className="space-y-2">
          {(mode === 'group'
            ? rows.filter((row) => selected.includes(row.name))
            : rows
          ).map(renderRow)}
          {mode === 'group' &&
            rows.some((row) => !selected.includes(row.name)) && (
              <div className="space-y-2">
                <Button
                  variant="outline"
                  size="sm"
                  className="w-full justify-between text-xs [&_svg]:h-3.5 [&_svg]:w-3.5"
                  aria-expanded={showAvailable}
                  onClick={() => setShowAvailable((open) => !open)}
                >
                  <span className="inline-flex items-center gap-2">
                    <Plus className="h-4 w-4" />
                    Add secrets
                  </span>
                  <ChevronDown
                    className={`h-4 w-4 transition-transform ${showAvailable ? 'rotate-180' : ''}`}
                  />
                </Button>
                {showAvailable && (
                  <div
                    role="group"
                    aria-label="Available secrets"
                    className="space-y-2"
                  >
                    {rows
                      .filter((row) => !selected.includes(row.name))
                      .map(renderRow)}
                  </div>
                )}
              </div>
            )}
          {missing.map((n) => (
            <div key={`missing:${n}`} className="flex items-center gap-2 py-3">
              <span className="min-w-0 flex-1 break-all text-sm">
                {n}
                <span className="ml-2 text-xs text-muted-foreground">
                  Unavailable
                </span>
              </span>
              <Button
                variant="ghost"
                size="sm"
                disabled={!!busy || !canWrite}
                onClick={() => void toggle({ name: n })}
              >
                Remove selection
              </Button>
            </div>
          ))}
          {!rows.length &&
            !missing.length &&
            (mode === 'group' || (mode === 'project' && isVault)) && (
              <p className="py-4 text-sm text-muted-foreground">
                {mode === 'project'
                  ? 'No Vault secrets available to you.'
                  : 'Add shared secrets in Vault first.'}
              </p>
            )}
        </div>
      )}
      {canManage && !editor && (
        <div className="border-t border-border pt-3">
          <Button
            size="lg"
            variant="outline"
            className="h-12 w-full text-sm"
            disabled={!!busy}
            onClick={() => {
              setName('')
              setValue('')
              setEditor(true)
            }}
          >
            <Plus className="h-4 w-4" />
            Add secret
          </Button>
        </div>
      )}
      {editor && canManage && (
        <form
          className="space-y-2 rounded-lg border border-border p-3"
          onSubmit={(e) => {
            e.preventDefault()
            void save()
          }}
        >
          <p className="text-xs text-muted-foreground">
            Paste the new value here. Saving it won’t disable the old secret in
            the original service.
          </p>
          <Input
            aria-label="Secret name"
            placeholder="SECRET_NAME"
            value={name}
            onChange={(e) => setName(e.target.value.toUpperCase())}
            disabled={!!busy}
          />
          <Input
            aria-label="Secret value"
            placeholder="Secret value"
            type="password"
            autoComplete="new-password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            disabled={!!busy}
          />
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="ghost"
              disabled={!!busy}
              onClick={() => {
                setEditor(false)
                setValue('')
              }}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={!!busy || !name.trim() || !value}>
              {busy ? 'Saving…' : 'Save'}
            </Button>
          </div>
        </form>
      )}
      <ConfirmationDialog
        isOpen={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => void remove()}
        title={`Delete ${pendingDelete?.name ?? 'secret'}?`}
        message={
          mode === 'vault'
            ? 'Deletes the secret from Vault and removes group access. To stop an old copy from working, disable it in the original service.'
            : 'Deletes the secret from this project. To stop an old copy from working, disable it in the original service.'
        }
        confirmText="Delete"
        type="danger"
        isLoading={!!busy}
      />
    </section>
  )
}
export default SecretSelectionSection
