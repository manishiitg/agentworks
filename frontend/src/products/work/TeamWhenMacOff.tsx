import { useCallback, useEffect, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'

export type TeamCopyStatus = {
  when_mac_off: 'team_copy' | 'wait'
  ready: boolean
  answered_today: number
  waiting: number
  mac_on: boolean
  needs_login?: string[]
  people?: { user_id: string; name: string; mac_on: boolean }[]
}

const CHOICES: { value: TeamCopyStatus['when_mac_off']; label: string }[] = [
  { value: 'team_copy', label: 'Answer from the team copy' },
  { value: 'wait', label: 'Wait for my Mac' },
]

/** The Team tab's "When your Mac is off" state (owner, 4 Nov, case B3): the server decides and keeps it; the screen
 * shows it and sends the owner's choice. */
export function useTeamCopyStatus(projectId: string, connection: string) {
  const [status, setStatus] = useState<TeamCopyStatus | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let live = true
    const load = () => desktopRequest<{ team_copy?: TeamCopyStatus }>(endpoint, 'POST', { action: 'team_copy' })
      .then(data => { if (live && data.team_copy) { setStatus(data.team_copy); setError('') } })
      .catch(cause => { if (live) setError(cause instanceof Error ? cause.message : 'The team copy could not be read.') })
    void load()
    const timer = window.setInterval(() => { void load() }, 30000)
    return () => { live = false; window.clearInterval(timer) }
  }, [endpoint])
  const choose = useCallback(async (value: TeamCopyStatus['when_mac_off']) => {
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<{ team_copy?: TeamCopyStatus }>(endpoint, 'POST', { action: 'team_copy', when_mac_off: value })
      if (data.team_copy) setStatus(data.team_copy)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Your choice could not be saved. Try again.')
    } finally {
      setBusy(false)
    }
  }, [endpoint])
  return { status, error, busy, choose }
}

/** The tab's one line on where things live, as the server's state has it. */
export function TeamDataLine({ status, network }: { status: TeamCopyStatus | null; network: string }) {
  const off = status?.when_mac_off === 'wait' ? 'and their messages wait for it while it is off.' : 'and from the team copy while it is off.'
  return <p className="mt-4 flex gap-2 rounded-[10px] bg-violet-500/10 px-3 py-2 text-[13px] text-violet-800 dark:text-violet-200">
    <span aria-hidden="true">👥</span><span>People work with you here. The shared folder and the team's chats are kept on {network}. Your agents answer from this Mac while it is on, {off}</span>
  </p>
}

/** "When your Mac is off" and the status line, right after the shared folder. */
export function TeamWhenMacOff({ status, error, busy, choose, network }: ReturnType<typeof useTeamCopyStatus> & { network: string }) {
  if (!status) return error ? <p className="mt-3 text-sm text-destructive">{error}</p> : null
  const now = [status.mac_on ? '💻 your Mac is on' : '💻 your Mac is off',
    ...(status.people || []).map(person => `${person.name}'s Mac is ${person.mac_on ? 'on' : 'off'}`),
    status.answered_today === 1 ? '1 question answered by the team copy today' : `${status.answered_today} questions answered by the team copy today`]
  if (status.waiting > 0) now.push(status.waiting === 1 ? '1 message waiting for your Mac' : `${status.waiting} messages waiting for your Mac`)
  return <section aria-label="When your Mac is off">
    <div className="flex flex-wrap items-center gap-2.5 border-t border-border px-0.5 py-2.5 text-sm">
      <b className="font-semibold">When your Mac is off</b>
      <div role="radiogroup" aria-label="When your Mac is off" className="ml-auto flex overflow-hidden rounded-full border border-border text-xs">
        {CHOICES.map(choice => <button key={choice.value} type="button" role="radio" aria-checked={status.when_mac_off === choice.value} disabled={busy} onClick={() => { if (status.when_mac_off !== choice.value) void choose(choice.value) }}
          className={`px-2.5 py-0.5 ${status.when_mac_off === choice.value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{choice.label}</button>)}
      </div>
      <p className="basis-full text-[12.5px] text-muted-foreground">{status.when_mac_off === 'team_copy'
        ? `Team copy: your shared agents keep answering from ${network} with their instructions, skills, memory, the team folder and the chat so far, but not the rest of your Mac. When your Mac is back, your agents read what was asked and answered.${status.ready ? '' : ' It is getting ready; until then, messages wait for your Mac.'}`
        : 'Messages to your shared agents wait while your Mac is off, and are answered when it is back.'}</p>
      {status.when_mac_off === 'team_copy' && !!status.needs_login?.length && <p className="basis-full text-[12.5px] text-amber-600">Sign in to {status.needs_login.join(' and ')} on {network} once so your team copy can answer; until then, messages wait for your Mac.</p>}
    </div>
    <p className="border-t border-border px-0.5 py-2.5 text-[12.5px] text-muted-foreground">Right now: {now.join(' · ')}</p>
    {error && <p className="text-[12.5px] text-destructive">{error}</p>}
  </section>
}
