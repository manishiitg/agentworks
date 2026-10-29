import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Loader2, AlertCircle, Users, Ban, ChevronDown, ChevronRight, Search, Copy, Plug } from 'lucide-react'
import { authApi, type AdminUser, type AdminUserWrite } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { SettingsCard, SettingsEmpty } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { Badge } from '../ui/badge'
import { Input } from '../ui/Input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select'
import { enabledProductSurfaces, PRODUCT_SURFACE_LABELS, isProductSurface } from '../../products/productSurfaceConfig'
import { selectableProducts } from './selectableProducts'
import { sharedAccountLabel } from '../../utils/tokenLimits'
import { allowedModelsText } from '../../utils/allowedModels'
import { formatTokens } from '../../utils/formatTokens'

// One role per account. The server stamps `role` and dual-writes the legacy
// booleans; both are sent so older servers (which ignore `role`) enforce
// the same access. Records without a stamped role map from the booleans.
type Role = 'admin' | 'creator' | 'editor' | 'viewer'
const ROLES: { value: Role; label: string; hint: string }[] = [
  { value: 'viewer', label: 'Member', hint: 'Runs and uses what is shared with them. Creates and edits nothing.' },
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

const CREATABLE_PRODUCTS = ['agentworks', 'relays', 'work', 'code', 'sparkquill']
const productLabel = (id: string) => isProductSurface(id) ? PRODUCT_SURFACE_LABELS[id] : id === 'finance' ? 'Finance' : id
const limitLabel = (n?: number) => n && n > 0 ? formatTokens(n) : 'Unlimited'
const overrideLabel = (n?: number) => n && n < 0 ? 'Unlimited' : n ? formatTokens(n) : 'Account default'
const signInLabel = (u: AdminUser) => u.invited || (u.provider && u.provider !== 'password' && u.provider !== 'simple') ? 'Company sign-in' : u.has_password ? 'Password sign-in' : 'No password set'

/** Account overview. Detailed updates belong to the existing administrator MCP tools. */
const UsersAdminPanel: React.FC<{ vaultOnly?: boolean }> = ({ vaultOnly = false }) => {
  const me = useAuthStore(s => s.user)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [products, setProducts] = useState<string[]>([])
  const [sharedProducts, setSharedProducts] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [copied, setCopied] = useState<string | null>(null)
  const loadSeq = useRef(0)
  const refresh = useCallback(async () => {
    const seq = ++loadSeq.current
    setLoading(true); setError(null)
    try {
      const response = await authApi.listAdminUsers()
      if (seq !== loadSeq.current) return
      setUsers(response.users || [])
      setProducts(selectableProducts(response.products || [], enabledProductSurfaces()))
      setSharedProducts((response.shared_products || []).filter(p => isProductSurface(p) && enabledProductSurfaces().includes(p)))
    } catch (err) {
      if (seq !== loadSeq.current) return
      const message = err instanceof Error ? err.message : String(err)
      setError(/timeout/i.test(message) ? 'Loading the accounts took longer than 15 seconds. Check your connection and try again.' : message)
    } finally { if (seq === loadSeq.current) setLoading(false) }
  }, [])
  useEffect(() => { void refresh(); return () => { loadSeq.current++ } }, [refresh])
  const run = async (id: string, patch: AdminUserWrite) => {
    setBusyId(id); setError(null)
    try { await authApi.updateAdminUser(id, patch); await refresh() }
    catch (err) { setError(err instanceof Error ? err.message : String(err)) }
    finally { setBusyId(null) }
  }
  const sorted = useMemo(() => [...users].sort((a, b) => a.username.localeCompare(b.username)), [users])
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? sorted.filter(u => `${u.username} ${u.email || ''}`.toLowerCase().includes(q)) : sorted
  }, [sorted, query])
  const accessible = (u: AdminUser, role: Role) => role === 'admin' || role === 'creator' && u.products.length === 0 ? products : u.products
  const copyAdminRequest = async (u: AdminUser) => {
    try {
      await navigator.clipboard.writeText(`Manage the AgentWorks account ${u.username}${u.email ? ` (${u.email})` : ''}, user_id ${u.id}. Use the admin MCP tools to inspect its access and AI usage, then help me update its detailed settings. Use the currently available admin MCP tools for access, password resets, deletion, AI usage, token limits and model restrictions. Keep unrelated settings unchanged.`)
      setCopied(u.id)
    } catch { setError('Could not copy the request. Open your connected AI app and ask it to manage this account.') }
  }
  return <div className="space-y-4">
    <SettingsCard icon={<Users className="h-4 w-4 text-primary" />} title="People" count={`${sorted.length} ${sorted.length === 1 ? 'account' : 'accounts'}`}
      description={vaultOnly ? undefined : 'Review access here. Update detailed permissions, AI limits and account settings through your connected AI app.'}>
      {!vaultOnly && <div className="mb-4 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <p>Basic updates are available below. New people are added by your server administrator.</p>
        <Button size="sm" variant="outline" onClick={() => openWorkspaceSettings('mcp')}><Plug className="mr-1.5 h-3.5 w-3.5" />Connect AI app</Button>
      </div>}
      {!vaultOnly && sharedProducts.length > 0 && <p className="mb-4 text-xs text-muted-foreground">Available to everyone: {sharedProducts.map(productLabel).join(', ')}. Access to folders, connections and agents follows their sharing settings.</p>}
      {error && <div role="alert" className="mb-3 flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" /><span className="flex-1">{error}</span><Button size="sm" variant="outline" onClick={() => void refresh()}>Retry</Button>
      </div>}
      {loading ? <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading accounts…</div>
        : !sorted.length ? <SettingsEmpty>No accounts yet. New people are added by your server administrator.</SettingsEmpty>
        : <div className="space-y-3">
          <div className="relative max-w-sm"><Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" /><Input value={query} onChange={e => setQuery(e.target.value)} placeholder="Search people" aria-label="Search people" className="h-8 pl-8 text-sm" /></div>
          <ul className="divide-y divide-border rounded-lg border border-border">{shown.map(u => {
            const role = roleOf(u), isMe = u.id === me?.id, busy = busyId === u.id, open = expanded === u.id && !vaultOnly
            const apps = accessible(u, role)
            const create = role === 'admin' ? products.filter(p => CREATABLE_PRODUCTS.includes(p)) : (u.create_products ?? (role === 'creator' ? apps.filter(p => CREATABLE_PRODUCTS.includes(p)) : [])).filter(p => apps.includes(p))
            const extras = [u.crew_reviewer && 'Crew reviewer', u.vault_reader && 'Vault audit'].filter(Boolean)
            return <li key={u.id} className={u.disabled ? 'opacity-60' : ''}>
              <Button type="button" variant="ghost" disabled={vaultOnly} onClick={() => setExpanded(open ? null : u.id)} aria-expanded={open} aria-label={`${open ? 'Close' : 'Details for'} ${u.username}`}
                className="h-auto w-full justify-start gap-3 rounded-none px-3 py-3 text-left font-normal hover:bg-muted/40 disabled:cursor-default disabled:opacity-100">
                {!vaultOnly && (open ? <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" /> : <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />)}
                <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{u.username}{isMe && <span className="ml-1 text-[11px] text-muted-foreground">(you)</span>}</span><span className="block truncate text-xs text-muted-foreground">{u.email || signInLabel(u)}</span></span>
                <span className="hidden max-w-[35%] truncate text-xs text-muted-foreground sm:block">{role === 'admin' || role === 'creator' && !u.products.length ? 'All products' : apps.filter(p => products.includes(p)).map(productLabel).join(', ') || 'No additional products'}</span>
                <Badge variant="secondary">{ROLES.find(r => r.value === role)?.label}</Badge>
                {u.disabled ? <Badge variant="outline" className="text-destructive"><Ban className="mr-1 h-3 w-3" />Off</Badge> : u.invited ? <span className="hidden text-xs text-muted-foreground sm:inline">Not signed in</span> : null}
              </Button>
              {open && <div className="space-y-4 border-t border-border bg-muted/10 p-4">
                <div className="flex flex-wrap items-center gap-3">
                  <label className="flex items-center gap-2 text-xs text-muted-foreground">Role<RolePicker value={role} label={`Role for ${u.username}`} disabled={busy || isMe && role === 'admin'} title={isMe && role === 'admin' ? 'You cannot remove your own admin access' : undefined} onChange={next => { void run(u.id, roleFields(next)) }} /></label>
                  {!isMe && <Button size="sm" variant="outline" disabled={busy} onClick={() => { void run(u.id, { disabled: !u.disabled }) }}>{u.disabled ? 'Enable account' : 'Disable account'}</Button>}
                  <Button size="sm" variant="ghost" className="sm:ml-auto" onClick={() => { void copyAdminRequest(u) }}><Copy className="mr-1.5 h-3.5 w-3.5" />{copied === u.id ? 'Copied' : 'Copy admin request'}</Button>
                  {busy && <Loader2 role="status" className="h-4 w-4 animate-spin" aria-label="Saving account" />}
                </div>
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs"><span className="text-muted-foreground">Additional product access</span>{role === 'admin' ? <span>All products</span> : products.map(product => <label key={product} className="inline-flex items-center gap-1.5"><Checkbox disabled={busy} checked={apps.includes(product)} aria-label={`${productLabel(product)} for ${u.username}`} onCheckedChange={() => { void run(u.id, { products: apps.includes(product) ? apps.filter(p => p !== product) : [...apps, product] }) }} />{productLabel(product)}</label>)}</div>
                <details className="border-t border-border pt-3"><summary className="cursor-pointer text-xs text-muted-foreground">Access and AI usage</summary>
                  <dl className="mt-3 grid gap-x-5 gap-y-2 text-xs sm:grid-cols-2" aria-label={`Access summary for ${u.username}`}>
                    <div><dt className="text-muted-foreground">Can create in</dt><dd>{create.map(productLabel).join(', ') || 'No products'}</dd></div>
                    <div><dt className="text-muted-foreground">Additional access</dt><dd>{extras.join(', ') || 'None'}</dd></div>
                    <div><dt className="text-muted-foreground">AI limits on shared accounts</dt><dd>Daily {limitLabel(u.token_limits?.daily)} · Weekly {limitLabel(u.token_limits?.weekly)}</dd></div>
                    <div><dt className="text-muted-foreground">AI usage</dt><dd>{u.token_usage ? `Today ${formatTokens(u.token_usage.daily_used)} · This week ${formatTokens(u.token_usage.weekly_used)}` : 'Not available'}</dd></div>
                    <div><dt className="text-muted-foreground">Sign-in</dt><dd>{signInLabel(u)}</dd></div>
                    {Object.entries(u.account_token_limits || {}).map(([provider, limits]) => <div key={`limits:${provider}`}><dt className="text-muted-foreground">{sharedAccountLabel(provider)} limits</dt><dd>Daily {overrideLabel(limits?.daily)} · Weekly {overrideLabel(limits?.weekly)}</dd></div>)}
                    {Object.entries(u.account_allowed_models || {}).map(([provider, models]) => <div key={`models:${provider}`}><dt className="text-muted-foreground">{sharedAccountLabel(provider)} models</dt><dd>{allowedModelsText(models)}</dd></div>)}
                  </dl>
                  <p className="mt-3 text-xs text-muted-foreground">Use admin MCP to change these settings, reset a password or delete an account.</p>
                </details>
              </div>}
            </li>
          })}{!shown.length && <li className="px-3 py-4 text-xs text-muted-foreground">No one matches “{query}”.</li>}</ul>
        </div>}
    </SettingsCard>
  </div>
}

export default UsersAdminPanel
