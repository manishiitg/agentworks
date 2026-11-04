import { useEffect, useState } from 'react'
import { desktopRequest } from '../../services/desktopConnections'

type TeamCopyStatus = {
  when_mac_off: 'team_copy' | 'wait'
  ready: boolean
  answered_today: number
  waiting: number
  mac_on: boolean
  people?: { user_id: string; name: string; mac_on: boolean }[]
}

const CHOICES: { value: TeamCopyStatus['when_mac_off']; label: string }[] = [
  { value: 'team_copy', label: 'Answer from the team copy' },
  { value: 'wait', label: 'Wait for my Mac' },
]

/** The Team tab's "When your Mac is off" (owner, 4 Nov, case B3): the shared agents' team copy answers, or messages
 * wait for the Mac. The server decides and keeps the state; this shows it and sends the owner's choice. */
export function TeamWhenMacOff({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
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
  const choose = async (value: TeamCopyStatus['when_mac_off']) => {
    if (busy || status?.when_mac_off === value) return
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<{ team_copy?: TeamCopyStatus }>(endpoint, 'POST', { action: 'team_copy', when_mac_off: value })
      if (data.team_copy) setStatus(data.team_copy)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Your choice could not be saved. Try again.')
    } finally {
      setBusy(false)
    }
  }
  if (!status) return error ? <p className="mt-4 text-sm text-destructive">{error}</p> : null
  const now = [status.mac_on ? 'your Mac is on' : 'your Mac is off',
    ...(status.people || []).map(person => `${person.name}'s Mac is ${person.mac_on ? 'on' : 'off'}`),
    status.answered_today === 1 ? '1 question answered by the team copy today' : `${status.answered_today} questions answered by the team copy today`]
  if (status.waiting > 0) now.push(status.waiting === 1 ? '1 message waiting for your Mac' : `${status.waiting} messages waiting for your Mac`)
  return <section aria-label="When your Mac is off" className="mt-4 space-y-2 rounded-xl border border-border px-4 py-3">
    <p className="text-[13px] text-muted-foreground">The shared folder and the team's chats are kept on {network}. Your agents answer from this Mac while it is on, and from the team copy while it is off.</p>
    <p className="text-sm font-semibold">When your Mac is off</p>
    <div role="radiogroup" aria-label="When your Mac is off" className="flex flex-wrap gap-2">
      {CHOICES.map(choice => <button key={choice.value} type="button" role="radio" aria-checked={status.when_mac_off === choice.value} disabled={busy} onClick={() => { void choose(choice.value) }}
        className={`rounded-full border px-3 py-1 text-[13px] ${status.when_mac_off === choice.value ? 'border-primary bg-primary/10 font-medium text-foreground' : 'border-border text-muted-foreground hover:bg-muted'}`}>{choice.label}</button>)}
    </div>
    <p className="text-[13px] text-muted-foreground">{status.when_mac_off === 'team_copy'
      ? `Team copy: your shared agents keep answering from ${network} with their instructions, skills, memory, the team folder and the chat so far, but not the rest of your Mac. When your Mac is back, your agents read what was asked and answered.${status.ready ? '' : ' It is getting ready; until then, messages wait for your Mac.'}`
      : 'Messages to your shared agents wait while your Mac is off, and are answered when it is back.'}</p>
    <p className="text-[13px] text-muted-foreground">Right now: {now.join(' · ')}</p>
    {error && <p className="text-[13px] text-destructive">{error}</p>}
  </section>
}
