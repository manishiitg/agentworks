import { useEffect, useSyncExternalStore } from 'react'
import type { AccountLimitsView } from '../../services/llm-config-api'
import { getAuthToken } from '../../services/api'
import { locationApiBase } from '../../stores/useConnectedWorkStore'

/**
 * Whether each AI account works, how much of its plan is used and when that resets, shown on the AI accounts page
 * (#3433). The server decides every figure: /api/provider-accounts/limits on this Mac or server, and on each
 * connected server for the company accounts used from it. One request per place per page visit (it may start the
 * server's own usage check); Refresh asks again.
 */
type PlaceLimits = { accounts?: AccountLimitsView[]; failed?: boolean }
const places = new Map<string, PlaceLimits>()
const loading = new Set<string>()
const listeners = new Set<() => void>()
let version = 0
const changed = () => { version++; listeners.forEach(listener => listener()) }

function load(place: string) {
  if (loading.has(place)) return
  loading.add(place)
  void fetch(`${locationApiBase(place)}/api/provider-accounts/limits`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(20_000) })
    .then(response => response.ok ? response.json() as Promise<{ accounts?: AccountLimitsView[] }> : Promise.reject(new Error('unavailable')))
    .then(data => places.set(place, { accounts: data.accounts ?? [] }), () => places.set(place, { ...places.get(place), failed: true }))
    .finally(() => { loading.delete(place); changed() })
}

/** Ask every place again (the page's Refresh button). */
export function refreshAccountHealth() { for (const place of places.keys()) load(place); changed() }

function useAccountLimits(placeIds: string[]): Map<string, PlaceLimits> {
  useSyncExternalStore(listener => { listeners.add(listener); return () => listeners.delete(listener) }, () => version)
  const key = placeIds.join(',')
  useEffect(() => { for (const place of key.split(',').filter(Boolean)) if (!places.has(place)) load(place) }, [key])
  return places
}

type Health = { tone: 'ok' | 'warning' | 'bad' | 'none'; text: string }
const pct = (value: number) => Math.min(100, Math.max(0, Math.round(value)))
const when = (iso?: string) => {
  if (!iso) return ''
  const date = new Date(iso), today = new Date()
  const time = date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  if (date.toDateString() === today.toDateString()) return `today ${time}`
  return `${date.toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' })} ${time}`
}
const ago = (iso: string) => {
  const minutes = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60_000))
  return minutes < 1 ? 'just now' : minutes < 60 ? `${minutes} min ago` : minutes < 48 * 60 ? `${Math.round(minutes / 60)} h ago` : `${Math.round(minutes / 1440)} days ago`
}

function healthOf(view: AccountLimitsView): Health {
  if (view.sign_in?.needed) return { tone: 'bad', text: `Sign-in expired${view.sign_in.since ? ` ${when(view.sign_in.since)}` : ''} · chats on this account fail until someone signs in again` }
  const windows = view.snapshot?.windows ?? []
  const full = windows.find(window => window.used_percent >= 100)
  if (full) return { tone: 'bad', text: `Plan used up${full.resets_at ? ` until ${when(full.resets_at)}` : ''} · pick another model for now` }
  const checked = view.snapshot ? ` · checked ${ago(view.snapshot.checked_at)}` : ''
  if (windows.some(window => window.used_percent >= 80)) return { tone: 'warning', text: `Near plan limit${checked}` }
  if (view.snapshot) return { tone: 'ok', text: `Working${checked}` }
  return { tone: 'none', text: 'Plan use not reported by this app' }
}
const dot = { ok: 'bg-emerald-500', warning: 'bg-warning', bad: 'bg-destructive', none: 'bg-muted-foreground/50' }
const textTone = { ok: 'text-muted-foreground', warning: 'font-medium text-warning', bad: 'font-medium text-destructive', none: 'text-muted-foreground' }

/** One account's status line and plan-use bars. `place` is 'local' or a connected server's id. */
export function AccountHealthLine({ place = 'local', provider, accountId, onSignIn }: { place?: string; provider: string; accountId: string; onSignIn?: () => void }) {
  const limits = useAccountLimits([place]).get(place)
  const view = limits?.accounts?.find(item => item.provider === provider && item.account_id === accountId)
  if (!view) return null
  const health = healthOf(view)
  return <div data-testid="account-health" className="mt-1 text-xs">
    <p className={`flex items-center gap-1.5 ${textTone[health.tone]}`}><span className={`h-1.5 w-1.5 shrink-0 rounded-full ${dot[health.tone]}`} />{health.text}</p>
    {health.tone === 'bad' && view.sign_in?.needed && onSignIn && <button type="button" onClick={onSignIn} className="mt-1 font-medium text-destructive underline">Sign in again</button>}
    {!view.sign_in?.needed && (view.snapshot?.windows.length ?? 0) > 0 && <div className="mt-1 grid grid-cols-[max-content_minmax(4rem,9rem)_max-content] items-center gap-x-2 gap-y-0.5 text-muted-foreground">
      {view.snapshot!.windows.map((window, index) => <div key={`${window.label}:${index}`} className="contents">
        <span>{window.label}: {pct(window.used_percent)}%</span>
        <span className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden><span className={`block h-full rounded-full ${window.used_percent >= 100 ? 'bg-destructive' : window.used_percent >= 80 ? 'bg-warning' : 'bg-primary'}`} style={{ width: `${pct(window.used_percent)}%` }} /></span>
        <span>{window.resets_at ? `resets ${when(window.resets_at)}` : ''}</span>
      </div>)}
    </div>}
  </div>
}

/** One line under the page heading: what needs attention across this Mac and its connected servers. */
export function AccountHealthSummary({ places: placeList, names }: { places: { id: string; name: string }[]; names: Record<string, string> }) {
  const limits = useAccountLimits(placeList.map(place => place.id))
  const issues: { tone: Health['tone']; text: string }[] = []
  let known = 0
  for (const place of placeList) {
    const entry = limits.get(place.id)
    if (!entry?.accounts) continue
    known++
    for (const view of entry.accounts) {
      const health = healthOf(view)
      if (health.tone !== 'bad' && health.tone !== 'warning') continue
      const name = `${names[view.provider] || view.provider}${view.shared_server ? '' : ` (${view.name})`}${place.id === 'local' ? '' : ` on ${place.name}`}`
      issues.push({ tone: health.tone, text: view.sign_in?.needed ? `${name}: sign-in expired` : health.tone === 'bad' ? `${name}: plan used up` : `${name}: near plan limit` })
    }
  }
  const failed = placeList.filter(place => limits.get(place.id)?.failed && !limits.get(place.id)?.accounts).map(place => place.name)
  if (!known && !failed.length) return null
  const bad = issues.some(issue => issue.tone === 'bad')
  return <div role={issues.length ? 'alert' : 'status'} className={`rounded-lg border px-4 py-3 text-sm ${bad ? 'border-destructive/40 bg-destructive/10 text-destructive' : issues.length ? 'border-warning/40 bg-warning/10 text-warning' : 'border-border text-muted-foreground'}`}>
    {issues.length
      ? <>Needs attention: {issues.map(issue => issue.text).join(' · ')}</>
      : known ? <span className="flex items-center gap-2"><span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />No account needs attention</span> : null}
    {failed.length > 0 && <span className="block text-muted-foreground">Couldn't check {failed.join(', ')}; what you see there may be out of date.</span>}
  </div>
}
