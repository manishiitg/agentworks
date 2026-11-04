import { useEffect, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'

export type TeamCopyStatus = {
  ready: boolean
  record_at?: string
  answered_today: number
  waiting: number
  mac_on: boolean
  needs_login?: string[]
  people?: { user_id: string; name: string; mac_on: boolean }[]
}

/** The Team tab's team copy state (owner, 4 Nov, case B3): the server decides and keeps it; the screen only shows it.
 * A shared agent's team copy always answers while the Mac is off; there is no choice (the decisions log). */
export function useTeamCopyStatus(projectId: string, connection: string) {
  const [status, setStatus] = useState<TeamCopyStatus | null>(null)
  const [error, setError] = useState('')
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let live = true
    // Another Crew: nothing of the previous one shows while this one loads.
    setStatus(null); setError('')
    const load = () => desktopRequest<{ team_copy?: TeamCopyStatus }>(endpoint, 'POST', { action: 'team_copy' })
      .then(data => { if (live && data.team_copy) { setStatus(data.team_copy); setError('') } })
      .catch(cause => { if (live) setError(cause instanceof Error ? cause.message : 'The team copy could not be read.') })
    void load()
    const timer = window.setInterval(() => { void load() }, 30000)
    return () => { live = false; window.clearInterval(timer) }
  }, [endpoint])
  return { status, error }
}

/** When the copy last changed: the Mac checks every 2 minutes while it is on but sends only a change (record_at). */
function copiedAgo(at: string | undefined, macOn: boolean) {
  const time = at ? Date.parse(at) : NaN
  if (Number.isNaN(time)) return 'not copied yet'
  const prefix = macOn ? 'copy up to date · last changed' : 'last changed'
  const minutes = Math.max(0, Math.round((Date.now() - time) / 60000))
  if (minutes < 1) return `${prefix} just now`
  if (minutes < 60) return `${prefix} ${minutes} min ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${prefix} ${hours} h ago`
  const days = Math.round(hours / 24)
  return `${prefix} ${days === 1 ? '1 day' : `${days} days`} ago`
}

/** The tab's one line on where things live. */
export function TeamDataLine({ network }: { network: string }) {
  return <p className="mt-4 px-1 text-[13px] text-muted-foreground">
    <span>People work with you here. The shared folder and the team's chats are kept on {network}. Your agents answer from this Mac while it is on, and from the team copy while it is off.</span>
  </p>
}

/** What the team copy keeps, when it was last copied and what it did today, right after the shared folder. */
export function TeamWhenMacOff({ status, error, network }: ReturnType<typeof useTeamCopyStatus> & { network: string }) {
  if (!status) return error ? <p className="mt-3 text-sm text-destructive">{error}</p> : null
  const now = [copiedAgo(status.record_at, status.mac_on),
    status.answered_today === 1 ? '1 question answered today' : `${status.answered_today} questions answered today`,
    status.mac_on ? 'your Mac is on' : 'your Mac is off',
    ...(status.people || []).map(person => `${person.name}'s Mac is ${person.mac_on ? 'on' : 'off'}`)]
  if (status.waiting > 0) now.push(status.waiting === 1 ? '1 message waiting for your Mac' : `${status.waiting} messages waiting for your Mac`)
  return <section aria-label="When your Mac is off" className="border-t border-border px-0.5 py-2.5 text-[12.5px] text-muted-foreground">
    <p>When your Mac is off, the team copy answers. It is a copy of each shared agent on {network}: its instructions, skills, memory and chat so far, never the rest of your Mac. When your Mac is back, your agents read what was asked and answered.</p>
    <p className="mt-1">{now.join(' · ').replace(/^./, first => first.toUpperCase())}</p>
    {!status.ready && <p className="mt-1">Until the first copy arrives, messages wait for your Mac.</p>}
    {!!status.needs_login?.length && <p className="mt-1 text-amber-600">Sign in to {status.needs_login.join(' and ')} on {network} once so your team copy can answer; until then, messages wait for your Mac.</p>}
    {error && <p className="mt-1 text-destructive">{error}</p>}
  </section>
}
