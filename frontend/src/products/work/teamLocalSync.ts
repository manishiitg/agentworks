import { useEffect, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import { teamError, type TeamTarget } from './teamApi'

/** A sync round in progress, from the sync client itself (Mac status "progress"). */
export type SyncProgress = { direction: 'upload' | 'download'; files: number; bytes_done: number; bytes_total: number }

/** This Mac's copy of a team folder, from its sync client: the round in progress and the server-written line for it
 * (crew_team_desktop.go, worded like the website's). Polled every 2 s while a round runs, else every 10 s. */
export type LocalSync = { line?: string; problem?: boolean; attention?: boolean; progress?: SyncProgress | null }
export function useLocalSync(target?: TeamTarget, enabled = true): LocalSync | undefined {
  const [local, setLocal] = useState<LocalSync>()
  useEffect(() => {
    setLocal(undefined)
    if (!target?.connection || !enabled) return
    let active = true, timer = 0
    const load = async () => {
      let next: LocalSync | undefined
      try { next = await desktopRequest<LocalSync>(`/${encodeURIComponent(target.connection!)}/crew-teams/${encodeURIComponent(target.project)}/local`) } catch (e) { next = { line: `Can't sync: ${teamError(e)}`, problem: true } }
      if (!active) return
      setLocal(next)
      timer = window.setTimeout(() => void load(), next?.progress ? 2000 : 10000)
    }
    void load()
    return () => { active = false; window.clearTimeout(timer) }
  }, [target?.connection, target?.project, enabled])
  return local
}
