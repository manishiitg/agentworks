import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, Trash2, AlertCircle, Users, KeyRound, Ban, CheckCircle2, Mail } from 'lucide-react'
import { authApi, type AdminUser, type AdminUserWrite } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { SettingsCard, SettingsEmpty } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { Badge } from '../ui/badge'
import { Input } from '../ui/Input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select'
import { SecretField } from '../ui/SecretField'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { enabledProductSurfaces, PRODUCT_SURFACE_LABELS, isProductSurface } from '../../products/productSurfaceConfig'
import { selectableProducts } from './selectableProducts'
import { TOKEN_LIMIT_UNLIMITED, formatTokens, parseOverrideAmount, parseTokenAmount, sharedAccountLabel, shownOverrideLimit } from '../../utils/tokenLimits'
import { llmConfigService } from '../../services/llm-config-api'
import AllowedModelsEditor from '../providers/AllowedModelsEditor'
import { allowedModelsText } from '../../utils/allowedModels'

// One role per account. The server stamps `role` and dual-writes the legacy
// booleans; both are sent so older servers (which ignore `role`) enforce
// the same access. Records without a stamped role map from the booleans.
type Role = 'admin' | 'creator' | 'editor' | 'viewer'
const ROLES: { value: Role; label: string; hint: string }[] = [
  { value: 'viewer', label: 'Viewer', hint: 'Use shared work. No creating or editing.' },
  { value: 'editor', label: 'Editor', hint: 'Edit assigned workflows. No creating.' },
  { value: 'creator', label: 'Creator', hint: 'Create and own workflows and projects.' },
  { value: 'admin', label: 'Admin', hint: 'Manage users, products and all workflows.' },
]
/** Shared styled role menu for both invitations and existing accounts. */
function RolePicker({ value, onChange, label, disabled, title }: {
  value: Role; onChange: (role: Role) => void; label: string; disabled?: boolean; title?: string
}) {
  return (
    <Select value={value} onValueChange={(next) => onChange(next as Role)} disabled={disabled}>
      <SelectTrigger aria-label={label} title={title} className="min-w-[7rem] bg-background text-xs">
        <SelectValue>{ROLES.find((role) => role.value === value)?.label}</SelectValue>
      </SelectTrigger>
      <SelectContent className="w-72 max-w-[calc(100vw-2rem)]" align="start">
        {ROLES.map((role) => (
          <SelectItem key={role.value} value={role.value} textValue={role.label} className="py-2.5">
            <span className="block text-xs font-medium">{role.label}</span>
            <span className="mt-0.5 block whitespace-normal text-xs leading-relaxed text-muted-foreground">{role.hint}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
const roleOf = (u: { role?: Role; admin: boolean; can_create: boolean; can_edit: boolean }): Role => (
  u.role ?? (u.admin ? 'admin' : u.can_create ? 'creator' : u.can_edit ? 'editor' : 'viewer')
)
const roleFields = (r: Role): Pick<AdminUserWrite, 'role' | 'admin' | 'can_create' | 'can_edit'> => ({
  role: r,
  admin: r === 'admin',
  can_create: r === 'admin' || r === 'creator',
  can_edit: r !== 'viewer',
})

// The exact stored value, so a blur without an edit never rounds and saves it.
const shownLimit = (n?: number) => (n ? String(n) : '')

/**
 * One person's limits on the shared server accounts, with their use today and
 * this week. Saved on blur or Enter; empty is unlimited.
 */
function TokenLimitsCell({ user, disabled, onSave, accountProviders, onSaveAccount, accountModels, onSaveModels }: {
  user: AdminUser; disabled: boolean; onSave: (limits: { daily: number; weekly: number }) => void
  /** Providers with a shared server account, for adding a per-person override. */
  accountProviders: string[]
  onSaveAccount: (provider: string, limits: { daily: number; weekly: number }) => void
  /** Each shared account's own model list (absent or empty = all models). */
  accountModels: Record<string, string[] | undefined>
  onSaveModels: (provider: string, models: string[] | null) => void
}) {
  const limits = user.token_limits
  const usage = user.token_usage
  const [daily, setDaily] = useState(shownLimit(limits?.daily))
  const [weekly, setWeekly] = useState(shownLimit(limits?.weekly))
  useEffect(() => { setDaily(shownLimit(limits?.daily)); setWeekly(shownLimit(limits?.weekly)) }, [limits?.daily, limits?.weekly])
  const parsedDaily = parseTokenAmount(daily)
  const parsedWeekly = parseTokenAmount(weekly)
  const invalid = parsedDaily === null || parsedWeekly === null
  const save = () => {
    if (invalid) return
    if (parsedDaily === (limits?.daily || 0) && parsedWeekly === (limits?.weekly || 0)) return
    onSave({ daily: parsedDaily, weekly: parsedWeekly })
  }
  const used = (n: number | undefined, limit: number | undefined) => `${formatTokens(n)}${limit ? ` / ${formatTokens(limit)}` : ''}`
  const tone = usage?.state === 'over' ? 'text-destructive' : usage?.state === 'warning' ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'
  const field = (label: string, value: string, set: (v: string) => void) => (
    <label className="flex items-center gap-1.5">
      <span className="w-12 text-muted-foreground">{label}</span>
      <Input
        value={value}
        onChange={(e) => set(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => { if (e.key === 'Enter') save() }}
        disabled={disabled}
        placeholder="Unlimited"
        aria-label={`${label} token limit for ${user.username}`}
        className="h-7 w-24 text-xs"
      />
    </label>
  )
  return (
    <div className="space-y-1 text-xs">
      {field('Daily', daily, setDaily)}
      {field('Weekly', weekly, setWeekly)}
      {invalid && <p className="text-destructive">Use a number like 500k or 5M.</p>}
      {usage && (
        <p className={tone} title="Overall: tokens (input + output) on all shared server accounts together. Day and week are UTC; weeks start Monday.">
          All: today {used(usage.daily_used, usage.daily_limit)} · week {used(usage.weekly_used, usage.weekly_limit)}
        </p>
      )}
      <AccountLimitsList user={user} disabled={disabled} accountProviders={accountProviders} onSave={onSaveAccount} />
      <AccountModelsList user={user} disabled={disabled} accountProviders={accountProviders} accountModels={accountModels} onSave={onSaveModels} />
    </div>
  )
}

/**
 * This person's models on each shared account where an admin gave them their
 * own list (PLAT-714); every other account uses its own list (Providers →
 * the account → Models). A list replaces the account's for them, "All
 * models" lifts the account's limit for them, "Account default" removes it.
 */
function AccountModelsList({ user, disabled, accountProviders, accountModels, onSave }: {
  user: AdminUser; disabled: boolean; accountProviders: string[]
  accountModels: Record<string, string[] | undefined>
  onSave: (provider: string, models: string[] | null) => void
}) {
  const overrides = user.account_allowed_models || {}
  const [editing, setEditing] = useState<string | null>(null)
  const shown = Object.keys(overrides).sort()
  const addable = accountProviders.filter((p) => !shown.includes(p))
  const rows = editing && !shown.includes(editing) ? [...shown, editing] : shown
  return (
    <div className="space-y-0.5">
      {rows.map((provider) => {
        const label = sharedAccountLabel(provider)
        if (editing === provider) {
          return (
            <AllowedModelsEditor key={provider} provider={provider} value={overrides[provider]} disabled={disabled}
              person={{ name: user.username, accountText: allowedModelsText(accountModels[provider]) }}
              onSave={(models) => { onSave(provider, models); setEditing(null) }} onCancel={() => setEditing(null)} />
          )
        }
        return (
          <p key={provider} className="text-muted-foreground">
            <button type="button" disabled={disabled} onClick={() => setEditing(provider)} className="underline-offset-2 hover:underline disabled:no-underline"
              title={`Edit ${user.username}'s models on the shared ${label} account. Account default: ${allowedModelsText(accountModels[provider])}.`}>
              {label} models
            </button>
            {': '}{allowedModelsText(overrides[provider])} (own)
          </p>
        )
      })}
      {addable.length > 0 && !editing && (
        <select aria-label={`Set models on an account for ${user.username}`} disabled={disabled} value=""
          onChange={(e) => { if (e.target.value) setEditing(e.target.value) }}
          className="h-6 rounded border border-border bg-background px-1 text-xs text-muted-foreground">
          <option value="">+ Models on an account…</option>
          {addable.map((p) => <option key={p} value={p}>{sharedAccountLabel(p)}{accountModels[p]?.length ? ` (${allowedModelsText(accountModels[p])})` : ''}</option>)}
        </select>
      )}
    </div>
  )
}

/**
 * One line per shared account (PLAT-693): this person's use against the
 * account's limit, which is the account default (Providers → Limits) unless
 * an override is set here. Edit sets the override; empty fields fall back to
 * the default, and "Unlimited" (-1) removes the limit for this person even
 * when the account has a default.
 */
function AccountLimitsList({ user, disabled, accountProviders, onSave }: {
  user: AdminUser; disabled: boolean; accountProviders: string[]
  onSave: (provider: string, limits: { daily: number; weekly: number }) => void
}) {
  const accounts = user.token_usage?.accounts || {}
  const overrides = user.account_token_limits || {}
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState({ daily: '', weekly: '' })
  const shown = [...new Set([...Object.keys(accounts), ...Object.keys(overrides)])].sort()
  const addable = accountProviders.filter((p) => !shown.includes(p))
  const startEdit = (provider: string) => {
    const o = overrides[provider]
    setDraft({ daily: shownOverrideLimit(o?.daily), weekly: shownOverrideLimit(o?.weekly) })
    setEditing(provider)
  }
  const save = (provider: string) => {
    const daily = parseOverrideAmount(draft.daily)
    const weekly = parseOverrideAmount(draft.weekly)
    if (daily === null || weekly === null) return
    onSave(provider, { daily, weekly })
    setEditing(null)
  }
  const part = (n: number | undefined, limit: number | undefined) => `${formatTokens(n)}${limit ? `/${formatTokens(limit)}` : ''}`
  const rows = editing && !shown.includes(editing) ? [...shown, editing] : shown
  return (
    <div className="space-y-0.5">
      {rows.map((provider) => {
        const a = accounts[provider]
        const o = overrides[provider]
        const def = a?.default_limits
        const tone = a?.state === 'over' ? 'text-destructive' : a?.state === 'warning' ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'
        const label = a?.label || sharedAccountLabel(provider)
        if (editing === provider) {
          const invalid = parseOverrideAmount(draft.daily) === null || parseOverrideAmount(draft.weekly) === null
          return (
            <div key={provider} className="flex flex-wrap items-center gap-1">
              <span className="w-16 truncate">{label}</span>
              {(['daily', 'weekly'] as const).map((field) => (
                <Input key={field} value={draft[field]} disabled={disabled}
                  onChange={(e) => setDraft({ ...draft, [field]: e.target.value })}
                  onKeyDown={(e) => { if (e.key === 'Enter') save(provider); if (e.key === 'Escape') setEditing(null) }}
                  placeholder={def?.[field] ? `Default ${formatTokens(def[field])}` : 'Unlimited'}
                  title='A number like 500k or 5M; empty uses the default; "Unlimited" means no limit even with a default'
                  aria-label={`${label} ${field} token limit for ${user.username}`} className="h-6 w-20 text-xs" />
              ))}
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-xs" disabled={disabled}
                title={`No limit for ${user.username} on the shared ${label} account, even when it has a default`}
                onClick={() => setDraft({ daily: 'Unlimited', weekly: 'Unlimited' })}>Unlimited</Button>
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-xs" disabled={disabled || invalid} onClick={() => save(provider)}>Save</Button>
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-xs" onClick={() => setEditing(null)}>Cancel</Button>
            </div>
          )
        }
        const hasLimit = Boolean(a?.daily_limit || a?.weekly_limit)
        return (
          <p key={provider} className={tone}>
            <button type="button" disabled={disabled} onClick={() => startEdit(provider)} className="underline-offset-2 hover:underline disabled:no-underline"
              title={`Edit ${user.username}'s limit on the shared ${label} account. Default: ${def?.daily || def?.weekly ? [def?.daily ? `${formatTokens(def.daily)} a day` : '', def?.weekly ? `${formatTokens(def.weekly)} a week` : ''].filter(Boolean).join(', ') : 'none'}.`}>
              {label}{o ? (o.daily === TOKEN_LIMIT_UNLIMITED && o.weekly === TOKEN_LIMIT_UNLIMITED ? ' (unlimited)' : ' (own limit)') : ''}
            </button>
            {': '}
            {a ? <>today {part(a.daily_used, a.daily_limit)} · week {part(a.weekly_used, a.weekly_limit)}</> : 'no use'}
            {!hasLimit && a && ' · no limit'}
          </p>
        )
      })}
      {addable.length > 0 && !editing && (
        <select aria-label={`Set an account limit for ${user.username}`} disabled={disabled} value=""
          onChange={(e) => { if (e.target.value) startEdit(e.target.value) }}
          className="h-6 rounded border border-border bg-background px-1 text-xs text-muted-foreground">
          <option value="">+ Limit on an account…</option>
          {addable.map((p) => <option key={p} value={p}>{sharedAccountLabel(p)}</option>)}
        </select>
      )}
    </div>
  )
}

// Products in which an account creates something (a workflow, Relay, Crew, Code project, video).
const CREATABLE_PRODUCTS = ['agentworks', 'relays', 'work', 'code', 'video-studio', 'sparkquill']

const productLabel = (id: string) => isProductSurface(id) ? PRODUCT_SURFACE_LABELS[id] : id === 'finance' ? 'Finance' : id

interface UsersAdminPanelProps {
  /** Vault adds MCP consumers; platform roles/products are managed outside this view. */
  vaultOnly?: boolean
}

/**
 * Users & access: the admin page for the user directory. Set each account's
 * role and which products they may open, reset passwords, disable or delete.
 */
const UsersAdminPanel: React.FC<UsersAdminPanelProps> = ({ vaultOnly = false }) => {
  const me = useAuthStore((s) => s.user)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [products, setProducts] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  // Providers with a shared server account, for per-person account overrides (PLAT-693).
  const [accountProviders, setAccountProviders] = useState<string[]>([])
  // Each shared account's own model list, for the per-person model overrides (PLAT-714).
  const [accountModels, setAccountModels] = useState<Record<string, string[] | undefined>>({})
  useEffect(() => {
    if (vaultOnly) return
    let cancelled = false
    void Promise.resolve().then(() => llmConfigService.getProviderConnections())
      .then((records) => {
        if (cancelled) return
        const servers = records.filter((r) => r.relation === 'server' || r.id.startsWith('global:'))
        setAccountProviders([...new Set(servers.map((r) => r.provider))])
        setAccountModels(Object.fromEntries(servers.map((r) => [r.provider, (r.default_allowed_models !== undefined ? r.default_allowed_models : r.allowed_models) ?? undefined])))
      })
      .catch(() => undefined)
    return () => { cancelled = true }
  }, [vaultOnly])

  const [resetFor, setResetFor] = useState<AdminUser | null>(null)
  const [resetPassword, setResetPassword] = useState('')
  const [deleteFor, setDeleteFor] = useState<AdminUser | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const resp = await authApi.listAdminUsers()
      setUsers(resp.users || [])
      setProducts(selectableProducts(resp.products || [], enabledProductSurfaces()))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const run = useCallback(async (id: string | null, action: () => Promise<unknown>) => {
    setBusyId(id)
    setError(null)
    try {
      await action()
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyId(null)
    }
  }, [refresh])

  const toggleProduct = (list: string[], id: string) => (list.includes(id) ? list.filter((p) => p !== id) : [...list, id])
  // Per-product create permission (PLAT-767): the products an account may open that have something to create, and
  // where it may create now (no list yet: a creator everywhere it may open, an editor nowhere).
  const creatableFor = (u: AdminUser, role: Role) => products.filter((p) => CREATABLE_PRODUCTS.includes(p) && (u.products.includes(p) || (role === 'creator' && u.products.length === 0)))
  const createListFor = (u: AdminUser, role: Role) => u.create_products ?? (role === 'creator' ? creatableFor(u, role) : [])

  // Turning Code review on also opens the Code product (the inspector lives
  // there) for an account whose products are a restricted list.
  const codeReviewerPatch = (u: AdminUser, role: Role, next: boolean): AdminUserWrite => {
    const patch: AdminUserWrite = { code_reviewer: next }
    const restricted = role === 'viewer' || role === 'editor' || u.products.length > 0
    if (next && role !== 'admin' && restricted && products.includes('code') && !u.products.includes('code')) {
      patch.products = [...u.products, 'code']
    }
    return patch
  }

  const sorted = useMemo(() => [...users].sort((a, b) => a.username.localeCompare(b.username)), [users])

  return (
    <div className="[container-type:inline-size] space-y-5">
      <SettingsCard
        icon={<Users className="h-4 w-4 text-primary" />}
        title="Accounts"
        count={`${sorted.length} ${sorted.length === 1 ? 'account' : 'accounts'}`}
        description={vaultOnly ? undefined : 'Manage roles and product access. People are added by DevOps on the server.'}
      >
        {error && (
          <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">
            <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
            <span>{error}</span>
          </div>
        )}
        {loading ? (
          <div className="flex items-center gap-2 text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            <span>Loading accounts…</span>
          </div>
        ) : sorted.length === 0 ? (
          <SettingsEmpty>No accounts yet. DevOps adds people on the server.</SettingsEmpty>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-[11px] uppercase tracking-wide text-muted-foreground">
              <tr className="text-left">
                <th className="py-1 pr-3 font-semibold">User</th>
                <th className="py-1 pr-3 font-semibold">Role</th>
                <th className="py-1 pr-3 font-semibold">Products</th>
                {!vaultOnly && <th className="py-1 pr-3 font-semibold" title="Tokens on the shared server accounts, per UTC day and Monday-start week. Own accounts are never limited.">Shared-account tokens (UTC)</th>}
                <th className="py-1 pr-3 font-semibold">Status</th>
                <th className="py-1 font-semibold text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((u) => {
                const isMe = u.id === me?.id
                const busy = busyId === u.id
                const role = roleOf(u)
                return (
                  <tr key={u.id} className={`border-t border-border ${u.disabled ? 'opacity-60' : ''}`}>
                    <td className="py-2 pr-3 align-top">
                      <div className="font-medium">{u.username}{isMe && <span className="ml-1 text-[11px] text-muted-foreground">(you)</span>}</div>
                      <div className="text-[11px] text-muted-foreground">{u.email || '—'} · {u.invited ? 'signs in with SSO' : `${u.provider}${u.has_password ? '' : ' · no password'}`}</div>
                    </td>
                    <td className="py-2 pr-3 align-top">
                      {vaultOnly ? <span className="text-xs text-muted-foreground">{ROLES.find(r => r.value === role)?.label}</span> : <RolePicker
                        value={role}
                        label={`Role for ${u.username}`}
                        disabled={busy || (isMe && role === 'admin')}
                        title={isMe && role === 'admin' ? 'You cannot remove your own admin access' : undefined}
                        onChange={(next) => { void run(u.id, () => authApi.updateAdminUser(u.id, roleFields(next))) }}
                      />}
                      {!vaultOnly && role !== 'admin' && (
                        <label
                          className="mt-1.5 flex items-center gap-1.5 text-xs"
                          title="Reviews every Code workspace's cost, chats and files, read-only. Every view is recorded in the audit log."
                        >
                          <Checkbox
                            disabled={busy}
                            checked={u.code_reviewer === true}
                            onCheckedChange={() => { void run(u.id, () => authApi.updateAdminUser(u.id, codeReviewerPatch(u, role, u.code_reviewer !== true))) }}
                            aria-label={`Code reviewer for ${u.username}`}
                          />
                          Code reviewer
                        </label>
                      )}
                    </td>
                    <td className="py-2 pr-3 align-top">
                      {vaultOnly ? <span className="text-xs text-muted-foreground">{role === 'admin' || (role === 'creator' && u.products.length === 0) ? 'All products' : u.products.map(productLabel).join(', ') || 'None'}</span> : role === 'admin' ? (
                        <span className="text-xs text-muted-foreground">all</span>
                      ) : (
                        products.length === 1 ? (
                          <span className="text-xs text-muted-foreground">{productLabel(products[0])}</span>
                        ) : (
                        <div className="flex flex-wrap gap-2 text-xs">
                          {products.map((p) => (
                            <label key={p} className="inline-flex items-center gap-1.5">
                              <Checkbox
                                disabled={busy}
                                checked={u.products.includes(p)}
                                onCheckedChange={() => { void run(u.id, () => authApi.updateAdminUser(u.id, { products: toggleProduct(u.products, p) })) }}
                                aria-label={`${productLabel(p)} for ${u.username}`}
                              />
                              {productLabel(p)}
                            </label>
                          ))}
                          {role === 'creator' && u.products.length === 0 && <span className="text-muted-foreground">(all)</span>}
                          {(role === 'viewer' || role === 'editor') && u.products.length === 0 && <span className="text-muted-foreground">(none)</span>}
                        </div>
                        )
                      )}
                      {!vaultOnly && (role === 'creator' || role === 'editor') && creatableFor(u, role).length > 0 && (
                        <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs" title="Where this account may create new workflows, Relays, Crews or projects. Editing what already exists is not affected.">
                          <span className="text-muted-foreground">May create in:</span>
                          {creatableFor(u, role).map((p) => (
                            <label key={p} className="inline-flex items-center gap-1.5">
                              <Checkbox
                                disabled={busy}
                                checked={createListFor(u, role).includes(p)}
                                onCheckedChange={() => { void run(u.id, () => authApi.updateAdminUser(u.id, { create_products: toggleProduct(createListFor(u, role), p) })) }}
                                aria-label={`May create in ${productLabel(p)} for ${u.username}`}
                              />
                              {productLabel(p)}
                            </label>
                          ))}
                        </div>
                      )}
                    </td>
                    {!vaultOnly && (
                      <td className="py-2 pr-3 align-top">
                        <TokenLimitsCell
                          user={u}
                          disabled={busy}
                          onSave={(limits) => { void run(u.id, () => authApi.updateAdminUser(u.id, { token_limits: limits })) }}
                          accountProviders={accountProviders}
                          onSaveAccount={(provider, limits) => { void run(u.id, () => authApi.updateAdminUser(u.id, { account_token_limits: { [provider]: limits } })) }}
                          accountModels={accountModels}
                          onSaveModels={(provider, models) => { void run(u.id, () => authApi.updateAdminUser(u.id, { account_allowed_models: { [provider]: models } })) }}
                        />
                      </td>
                    )}
                    <td className="py-2 pr-3 align-top text-xs">
                      {u.disabled
                        ? <Badge variant="outline" className="text-destructive"><Ban className="mr-1 h-3 w-3" />Disabled</Badge>
                        : u.invited
                          ? <Badge variant="outline" title="Added by an administrator; not signed in yet"><Mail className="mr-1 h-3 w-3" />Added</Badge>
                          : <Badge variant="secondary"><CheckCircle2 className="mr-1 h-3 w-3" />Active</Badge>}
                    </td>
                    <td className="py-2 align-top">
                      <div className="flex items-center justify-end gap-1">
                        {busy && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          title="Set a new password"
                          disabled={busy}
                          onClick={() => { setResetFor(u); setResetPassword('') }}
                        >
                          <KeyRound className="h-3.5 w-3.5" />
                        </Button>
                        {!isMe && (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7"
                            title={u.disabled ? 'Enable account' : 'Disable account'}
                            disabled={busy}
                            onClick={() => { void run(u.id, () => authApi.updateAdminUser(u.id, { disabled: !u.disabled })) }}
                          >
                            {u.disabled ? <CheckCircle2 className="h-3.5 w-3.5" /> : <Ban className="h-3.5 h-3.5" />}
                          </Button>
                        )}
                        {!isMe && (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-destructive hover:bg-destructive/10"
                            title="Delete account (their files are kept)"
                            disabled={busy}
                            onClick={() => setDeleteFor(u)}
                          >
                            <Trash2 className="h-3.5 h-3.5" />
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </SettingsCard>

      {resetFor && (
        <SettingsCard
          icon={<KeyRound className="h-4 w-4 text-primary" />}
          title={`New password for ${resetFor.username}`}
          description="At least 8 characters. The old password stops working immediately."
        >
          <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
            <div className="flex-1">
              <SecretField
                label="New password"
                value={resetPassword}
                onChange={setResetPassword}
                placeholder="min 8 characters"
              />
            </div>
            <div className="flex gap-2">
              <Button
                disabled={resetPassword.length < 8 || busyId === resetFor.id}
                onClick={() => { const target = resetFor; void run(target.id, async () => { await authApi.updateAdminUser(target.id, { password: resetPassword }); setResetFor(null) }) }}
              >
                Save password
              </Button>
              <Button variant="outline" onClick={() => setResetFor(null)}>Cancel</Button>
            </div>
          </div>
        </SettingsCard>
      )}

      <ConfirmationDialog
        isOpen={deleteFor !== null}
        onClose={() => setDeleteFor(null)}
        onConfirm={() => { const target = deleteFor; setDeleteFor(null); if (target) void run(target.id, () => authApi.deleteAdminUser(target.id)) }}
        title={`Delete ${deleteFor?.username ?? 'account'}?`}
        message={deleteFor ? `Delete the account ${deleteFor.username}? Their files stay on disk. This cannot be undone.` : ''}
        confirmText="Delete account"
        type="danger"
        requireText={deleteFor?.username}
      />
    </div>
  )
}

export default UsersAdminPanel
