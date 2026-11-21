import { useCallback, useEffect, useRef, useState } from 'react'
import type { RunFolderInfo } from '../../services/api-types'

const WATCH_MS = 25_000
const LOOK_EVERY_MS = 2_000
const RESULT_MS = 30_000

const isRunning = (folder: RunFolderInfo) => folder.metadata?.status === 'running'
const newestFirst = (a: RunFolderInfo, b: RunFolderInfo) => (b.metadata?.created_at ?? '').localeCompare(a.metadata?.created_at ?? '')

/**
 * Follows a workflow's runs so a person can see what is happening. It looks again every couple of seconds while a run
 * is going, and for a short time after one was started (a run may not have written its first record yet). When the
 * run ends it keeps its result for a while, so the banner never just vanishes: `result` names the run that ended.
 */
export function useRunWatch(folders: RunFolderInfo[], refresh: () => Promise<void>) {
  const running = [...folders].filter(isRunning).sort(newestFirst)[0]
  const [watchUntil, setWatchUntil] = useState(0)
  const [result, setResult] = useState<string | null>(null)
  const [, setTick] = useState(0)
  const known = useRef<Set<string>>(new Set())
  const latest = useRef({ folders, running, watchUntil })
  latest.current = { folders, running, watchUntil }

  /** Call when a run was just started: the page then follows it from the first second. */
  const watch = useCallback(() => {
    known.current = new Set(latest.current.folders.map(folder => folder.name))
    setResult(null)
    setWatchUntil(Date.now() + WATCH_MS)
  }, [])

  const starting = !running && Date.now() < watchUntil

  useEffect(() => {
    if (!running && Date.now() >= watchUntil) return
    const timer = setInterval(() => {
      const { running: now, watchUntil: until } = latest.current
      if (!now && Date.now() >= until) { clearInterval(timer); setTick(tick => tick + 1); return }
      void refresh()
      setTick(tick => tick + 1)
    }, LOOK_EVERY_MS)
    return () => clearInterval(timer)
  }, [running?.name, watchUntil, refresh])

  // A run that was seen running and now is not has ended.
  const wasRunning = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (wasRunning.current && !running) setResult(wasRunning.current)
    wasRunning.current = running?.name
  }, [running?.name])

  // A short run can start and end between two looks: a new finished run after a start is its result too.
  useEffect(() => {
    if (running || Date.now() >= watchUntil) return
    const fresh = [...folders].sort(newestFirst).find(folder => !known.current.has(folder.name) && folder.metadata?.status && !isRunning(folder))
    if (fresh) { setResult(fresh.name); setWatchUntil(0) }
  }, [folders, running, watchUntil])

  useEffect(() => {
    if (!result) return
    const timer = setTimeout(() => setResult(null), RESULT_MS)
    return () => clearTimeout(timer)
  }, [result])

  return { running, starting, result, dismiss: () => setResult(null), watch }
}
