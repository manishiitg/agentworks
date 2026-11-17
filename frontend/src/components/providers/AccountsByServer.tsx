import { useEffect, useState, type ReactNode } from 'react'
import AccountLimits, { placeGet } from './AccountLimits'
import { hasDesktopConnections, locationApiBase, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../stores/useWorkspaceConnectionStore'
import { takeSubscriptionLimitsPlace } from '../../utils/subscriptionLimitsNavigation'

const placeName = (id: string, name: string) => id === 'local' ? 'This Mac' : name

/** Each place answers for itself (null = not answering); the Mac only groups the answers. */
export function useSignInNeededByPlace(): { id: string; name: string; count: number | null }[] {
  const connections = useConnectedWorkStore(state => state.connections)
  const [counts, setCounts] = useState<Record<string, number | null>>({})
  const [tick, setTick] = useState(0)
  useEffect(() => {
    void useConnectedWorkStore.getState().refresh()
    // Counts cost nothing on the server (no usage checks); recheck every two minutes.
    const timer = window.setInterval(() => setTick(value => value + 1), 120_000)
    return () => window.clearInterval(timer)
  }, [])
  const ids = connections.map(connection => connection.id).join(',')
  useEffect(() => {
    let live = true
    for (const connection of connections) {
      void placeGet<{ sign_in_needed?: number }>(locationApiBase(connection.id), '/api/provider-accounts/sign-in-needed')
        .then(data => data.sign_in_needed ?? 0, () => null)
        .then(count => { if (live) setCounts(state => ({ ...state, [connection.id]: count })) })
    }
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ids, tick])
  return connections.map(connection => ({ id: connection.id, name: placeName(connection.id, connection.name), count: counts[connection.id] ?? null }))
}

/**
 * Accounts and usage with a server picker on the Mac: All | This Mac | each connected server.
 * On the website there is one place, so it shows that server's accounts without a picker.
 * allView is shown under the server cards when All is picked.
 */
export default function AccountsByServer({ onOpenProvider, allView, defaultPlace = 'all' }: {
  onOpenProvider?: (provider: string) => void; allView?: ReactNode; defaultPlace?: string
}) {
  if (!hasDesktopConnections()) return allView ? <>{allView}</> : <AccountLimits onOpenProvider={onOpenProvider} />
  return <PlacePicker onOpenProvider={onOpenProvider} allView={allView} defaultPlace={defaultPlace} />
}

function PlacePicker({ onOpenProvider, allView, defaultPlace }: { onOpenProvider?: (provider: string) => void; allView?: ReactNode; defaultPlace: string }) {
  const places = useSignInNeededByPlace()
  const [picked, setPicked] = useState(() => takeSubscriptionLimitsPlace() || defaultPlace)
  useEffect(() => {
    const show = () => { const place = takeSubscriptionLimitsPlace(); if (place) setPicked(place) }
    window.addEventListener('show-subscription-limits', show)
    return () => window.removeEventListener('show-subscription-limits', show)
  }, [])
  const place = places.find(item => item.id === picked)
  const active = getRawActiveWorkspaceId()
  const tab = (id: string, label: string, alert = false) => <button key={id} type="button" role="tab" aria-selected={picked === id} onClick={() => setPicked(id)}
    className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm ${picked === id ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
    {label}{alert && <span aria-label="sign-in needed" className="h-1.5 w-1.5 rounded-full bg-destructive" />}
  </button>
  return <div className="space-y-5">
    <div role="tablist" aria-label="Choose a place" className="flex w-fit flex-wrap gap-1 rounded-lg bg-muted p-1">
      {tab('all', 'All')}
      {places.map(item => tab(item.id, item.name, Boolean(item.count)))}
    </div>
    {picked === 'all' || !place ? <>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {places.map(item => <button key={item.id} type="button" onClick={() => setPicked(item.id)} className="rounded-xl border border-border p-4 text-left hover:border-primary">
          <span className="block font-medium">{item.name}</span>
          <span className={`mt-2 inline-block rounded-full px-2 py-0.5 text-xs ${item.count === null ? 'bg-muted text-muted-foreground' : item.count ? 'bg-destructive/10 text-destructive' : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'}`}>
            {item.count === null ? 'Not answering' : item.count ? `${item.count} sign-in needed` : 'All signed in'}
          </span>
        </button>)}
      </div>
      {allView}
    </> : <AccountLimits key={place.id} base={locationApiBase(place.id)} placeName={place.name} macOnly={place.id === 'local'}
      onOpenProvider={onOpenProvider}
      signInHint={place.id === active ? undefined : `To sign in again, switch to ${place.name} and open AI accounts there.`} />}
  </div>
}
