import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Loader2, Trash2, AlertCircle, Users, KeyRound, Ban, CheckCircle2, UserPlus, Mail } from 'lucide-react'
import { authApi, type AdminUser, type AdminUserWrite } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { SettingsCard, SettingsEmpty } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/checkbox'
import { Badge } from '../ui/badge'
import { Input } from '../ui/Input'
import { SecretField } from '../ui/SecretField'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { enabledProductSurfaces } from '../../products/productSurfaceConfig'
import { selectableProducts } from './selectableProducts'

// One role per account. The server stamps `role` and dual-writes the legacy
// booleans; both are sent so older servers (which ignore `role`) enforce
// the same access. Records without a stamped role map from the booleans.
type Role = 'admin' | 'creator' | 'editor' | 'viewer'
const ROLES: { value: Role; label: string; hint: string }[] = [
  { value: 'viewer', label: 'Viewer', hint: 'Can chat, run and watch what is shared with them. Cannot create or edit anything.' },
  { value: 'editor', label: 'Editor', hint: 'Can own and edit assigned workflows, but cannot create new workflows.' },
  { value: 'creator', label: 'Creator', hint: 'Creates workflows and projects and owns what they create.' },
  { value: 'admin', label: 'Admin', hint: 'Creator, plus manages users and product access. Can open any workflow.' },
]
const roleOf = (u: { role?: Role; admin: boolean; can_create: boolean; can_edit: boolean }): Role => (
  u.role ?? (u.admin ? 'admin' : u.can_create ? 'creator' : u.can_edit ? 'editor' : 'viewer')
)
const roleFields = (r: Role): Pick<AdminUserWrite, 'role' | 'admin' | 'can_create' | 'can_edit'> => ({
  role: r,
  admin: r === 'admin',
  can_create: r === 'admin' || r === 'creator',
  can_edit: r !== 'viewer',
})

const PRODUCT_LABELS: Record<string, string> = {
  agentworks: 'Goals',
  work: 'Crew',
  code: 'Code',
  'video-studio': 'Video Studio',
  finance: 'Finance',
  dominion: 'Dominion',
  sparkquill: 'SparkQuill',
}
const productLabel = (id: string) => PRODUCT_LABELS[id] ?? id

/**
 * Users & access: the admin page for the user directory. Set each account's
 * role and which products they may open, reset passwords, disable or delete.
 */
const UsersAdminPanel: React.FC = () => {
  const me = useAuthStore((s) => s.user)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [products, setProducts] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)

  const [resetFor, setResetFor] = useState<AdminUser | null>(null)
  const [resetPassword, setResetPassword] = useState('')
  const [deleteFor, setDeleteFor] = useState<AdminUser | null>(null)

  // Add by email: no password; the person signs in with SSO using this
  // address and the account keeps the role and products set here.
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteRole, setInviteRole] = useState<Role>('viewer')
  const [inviteProducts, setInviteProducts] = useState<string[]>([])
  const [inviting, setInviting] = useState(false)
  // Shown after adding someone. Accounts are added here by an administrator: signing in never creates one.
  const [addedNotice, setAddedNotice] = useState<string | null>(null)
  const inviteEmailValid = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(inviteEmail.trim())

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

  const addByEmail = async () => {
    const email = inviteEmail.trim()
    setInviting(true)
    setError(null)
    try {
      const created = await authApi.createAdminUser({
        username: email,
        email,
        ...roleFields(inviteRole),
        // With one product there is nothing to choose: they get it.
        products: inviteRole === 'admin' ? [] : products.length === 1 ? products : inviteProducts,
      })
      setAddedNotice(`${created.email || email} was added. Ask them to sign in with Google using this address.`)
      setInviteEmail('')
      setInviteProducts([])
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setInviting(false)
    }
  }

  const sorted = useMemo(() => [...users].sort((a, b) => a.username.localeCompare(b.username)), [users])

  return (
    <div className="space-y-4">
      <SettingsCard
        icon={<UserPlus className="h-4 w-4 text-primary" />}
        title="Add a user"
        description="Add someone by email. There is no password: they sign in with SSO (for example Google) using this address, and the account keeps the role and products you set here. They show as Invited until their first sign-in."
      >
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <Input
              type="email"
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && inviteEmailValid && !inviting) void addByEmail() }}
              placeholder="name@example.com"
              aria-label="Email"
              className="sm:max-w-xs"
            />
            <select
              value={inviteRole}
              onChange={(e) => setInviteRole(e.target.value as Role)}
              aria-label="Role"
              className="px-2 py-1.5 text-sm bg-muted/40 border border-border rounded"
            >
              {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
            </select>
            <Button disabled={!inviteEmailValid || inviting} onClick={() => { void addByEmail() }}>
              {inviting ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : <UserPlus className="mr-1 h-3.5 w-3.5" />}
              Add user
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{ROLES.find((r) => r.value === inviteRole)?.hint}</p>
          {inviteRole !== 'admin' && products.length > 1 && (
            <div className="flex flex-wrap items-center gap-3 text-xs">
              <span className="text-muted-foreground">Products:</span>
              {products.map((p) => (
                <label key={p} className="inline-flex items-center gap-1.5">
                  <Checkbox
                    checked={inviteProducts.includes(p)}
                    onCheckedChange={() => setInviteProducts((list) => toggleProduct(list, p))}
                    aria-label={`${productLabel(p)} for the new user`}
                  />
                  {productLabel(p)}
                </label>
              ))}
              {inviteProducts.length === 0 && <span className="text-muted-foreground">{inviteRole === 'creator' ? '(none ticked: all)' : '(none ticked: none)'}</span>}
            </div>
          )}
          {addedNotice && (
            <div role="status" className="flex flex-wrap items-center gap-2 rounded-md border border-emerald-500/40 p-2 text-xs text-emerald-700 dark:text-emerald-300">
              <span className="min-w-0 flex-1">{addedNotice}</span>
              <Button variant="ghost" size="sm" onClick={() => setAddedNotice(null)}>Dismiss</Button>
            </div>
          )}
        </div>
      </SettingsCard>

      <SettingsCard
        icon={<Users className="h-4 w-4 text-primary" />}
        title="Accounts"
        count={`${sorted.length} ${sorted.length === 1 ? 'account' : 'accounts'}`}
        description="Everyone who can open this deployment, and what each account may do. A creator owns what they create; an editor may edit assigned workflows but cannot create new ones; a viewer only sees shared workflows. Product boxes decide which surfaces an account may open. A Code reviewer (any role) reviews every Code workspace's cost, chats and files, read-only, and every view is audited."
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
          <SettingsEmpty>No accounts yet. Add one above.</SettingsEmpty>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-[11px] uppercase tracking-wide text-muted-foreground">
              <tr className="text-left">
                <th className="py-1 pr-3 font-semibold">User</th>
                <th className="py-1 pr-3 font-semibold">Role</th>
                <th className="py-1 pr-3 font-semibold">Products</th>
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
                      <select
                        value={role}
                        disabled={busy || (isMe && role === 'admin')}
                        title={isMe && role === 'admin' ? 'You cannot remove your own admin access' : undefined}
                        onChange={(e) => { void run(u.id, () => authApi.updateAdminUser(u.id, roleFields(e.target.value as Role))) }}
                        className="px-2 py-1 text-xs bg-muted/40 border border-border rounded"
                      >
                        {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
                      </select>
                      {role !== 'admin' && (
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
                      {role === 'admin' ? (
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
                    </td>
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
