import { useEffect, useRef, useState } from 'react'
import { liveFeed, type LiveFeedKind, type LiveFeedStatus } from '../services/liveFeed'

export interface LiveRefetchOptions {
  // Notice kinds that should trigger a refetch.
  kinds: readonly LiveFeedKind[]
  // Workflow the panel shows (any path inside it); null = every workflow.
  workflow?: string | null
  project?: string | null
  scopeKey?: string
  // Refresh after external edits when the person returns to this view.
  refreshOnFocus?: boolean
  // Timer used only while the live feed is unavailable (the old poll rate).
  // 0 = no timer.
  fallbackMs: number
  // Slow check while the feed is live, for writes the server cannot see.
  // 0 = no timer.
  safetyMs?: number
  // Coalesce bursts: at most one refetch per this many ms (trailing call kept).
  minIntervalMs?: number
  enabled?: boolean
}

const DEFAULT_SAFETY_MS = 5 * 60_000

export function useLiveFeedStatus(): LiveFeedStatus {
  const [status, setStatus] = useState(liveFeed.getStatus())
  useEffect(() => liveFeed.onStatus(setStatus), [])
  return status
}

/**
 * Calls `refetch` when the live feed reports a matching change, instead of on
 * a fixed timer. Never refetches while the tab is hidden: a change seen then
 * is replayed once when the tab becomes visible. Falls back to `fallbackMs`
 * polling whenever the feed is not live.
 */
export function useLiveRefetch(refetch: () => void, options: LiveRefetchOptions): void {
  const { kinds, workflow = null, project = null, scopeKey, refreshOnFocus = false, fallbackMs, safetyMs = DEFAULT_SAFETY_MS, minIntervalMs = 0, enabled = true } = options
  const refetchRef = useRef(refetch)
  refetchRef.current = refetch
  const status = useLiveFeedStatus()
  const kindsKey = kinds.join(',')

  useEffect(() => {
    if (!enabled) return
    let dirtyWhileHidden = false
    let lastRun = 0
    let trailing: ReturnType<typeof setTimeout> | null = null

    const run = () => {
      if (document.hidden) {
        dirtyWhileHidden = true
        return
      }
      const wait = lastRun + minIntervalMs - Date.now()
      if (wait > 0) {
        trailing ??= setTimeout(() => { trailing = null; run() }, wait)
        return
      }
      lastRun = Date.now()
      refetchRef.current()
    }

    const unsubscribe = liveFeed.subscribe(kindsKey.split(',') as LiveFeedKind[], workflow, run, project)
    const onVisible = () => {
      if (!document.hidden && (dirtyWhileHidden || refreshOnFocus)) {
        dirtyWhileHidden = false
        run()
      }
    }
    if (refreshOnFocus) window.addEventListener('focus', run)
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      unsubscribe()
      window.removeEventListener('focus', run)
      document.removeEventListener('visibilitychange', onVisible)
      if (trailing) clearTimeout(trailing)
    }
  }, [enabled, kindsKey, workflow, project, scopeKey, refreshOnFocus, minIntervalMs])

  // Timer: the old poll rate while the feed is down, a slow safety check
  // while it is live. Hidden tabs skip ticks; returning to the tab refetches
  // once if the feed was down (changes may have been missed).
  const intervalMs = status === 'live' ? safetyMs : fallbackMs
  useEffect(() => {
    if (!enabled || intervalMs <= 0) return
    const tick = () => { if (!document.hidden) refetchRef.current() }
    const timer = window.setInterval(tick, intervalMs)
    const onVisible = () => { if (!document.hidden && status !== 'live') refetchRef.current() }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [enabled, intervalMs, status])
}
