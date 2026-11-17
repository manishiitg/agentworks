import { useEffect, useState } from 'react'
import { Cloud } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { AskButton, teamCard } from './TeamTab'

export type OnlineAgentPlacement = { tab: string; name: string; location: string }
export type OnlineAgent = {
  tab: string; name: string; program?: string; status?: {current_task?: string; current_step?: string; last_messages?: string[]}; activity?: string; last_active_label?: string; cli?: string; model_id?: string; reasoning_effort?: string; crew_name?: string
  automations: { kind: string; name: string; line: string; on: boolean }[]
}

type OnlineAgentStatus = { online_agents?: OnlineAgent[] }

/** Server placements and the server's current automations, not the disabled
 * schedule entries retained on the Mac after Run online. */
export function OnlineAgentsCard({ projectId, placements, connection, serverProject, network, website }: {
  projectId: string; placements?: Record<string, OnlineAgentPlacement>
  connection?: string; serverProject?: string; network: string; website: string
}) {
  const [remote, setRemote] = useState<OnlineAgent[]>()
  const [error, setError] = useState('')
  const [statusTab, setStatusTab] = useState('')
  useEffect(() => {
    let active = true
    const abort = new AbortController()
    const read = async () => {
      const route = `/api/crew-sync/${encodeURIComponent(connection && serverProject ? serverProject : projectId)}?online_agents=1`
      if (!connection) return (await api.get<OnlineAgentStatus>(route, { signal: abort.signal, skipSessionContext: true })).data
      const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}${route}`, {
        signal: abort.signal, headers: { Authorization: `Bearer ${getAuthToken() || ''}` },
      })
      if (!response.ok) throw new Error('Online agents unavailable')
      return response.json() as Promise<OnlineAgentStatus>
    }
    let reading = false, lastRead = 0
    const load = () => {
      if (document.hidden || reading || Date.now() - lastRead < 15000) return
      reading = true; lastRead = Date.now()
      void read().then(data => { if (active) { setRemote(data.online_agents || []); setError('') } })
        .catch(() => { if (active) setError('Could not load the online agents and their automations. Try again shortly.') })
        .finally(() => { reading = false })
    }
    load()
    const timer = window.setInterval(load, 15000)
    document.addEventListener('visibilitychange', load)
    return () => { active = false; window.clearInterval(timer); document.removeEventListener('visibilitychange', load); abort.abort() }
  }, [connection, serverProject, projectId])
  const current = remote
  const known = Object.values(placements || {}).filter(agent => agent.location === 'server').sort((a, b) => a.name.localeCompare(b.name))
  const rows: (Omit<OnlineAgent, 'automations'> & { automations?: OnlineAgent['automations'] })[] = current || known.map(agent => ({ ...agent, automations: undefined, program: undefined }))
  const review = 'Show this Crew’s online agents and which server they run on. Explain how Run online works and how to manage their schedules, webhooks and watchers. Read the current setup on the server, not the disabled entries on this Mac. Do not make changes unless I ask.'
  const run = 'Help me run an existing agent in this Crew online. Show my agents and ask which one I want. Then have that agent prepare its handover and choose the files and automations that should run on the server. Explain the server account choice and keep the original agent on this Mac. Use the existing Run online flow.'
  return <section aria-label="Online agents" className={teamCard}>
    <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">Online agents</h3><AskButton projectId={projectId} prompt={review} label="Ask AI" /></div>
    <p className="text-[12.5px] text-muted-foreground">These agents run on {network}, even when this Mac is off. Each has its own chat, memory and files.</p>
    {error && <p role="alert" className="text-xs text-amber-600">{error}</p>}
    {!current && !error && <p role="status" className="text-xs text-muted-foreground">Loading online agents…</p>}
    {current && !rows.length && !error && <p className="text-[12.5px] text-muted-foreground">No online agents yet. Choose Run online from an agent’s menu, or ask AI below.</p>}
    <div className="divide-y divide-border">
      {rows.map(agent => <div key={agent.tab} className="space-y-2 py-2.5">
        <div className="flex items-start gap-2.5">
          <Cloud aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1"><p className="break-words text-[13px] font-semibold">{agent.name}</p><p className="text-xs text-muted-foreground">{agent.cli || agent.program || (current ? 'Program unavailable' : 'Loading program…')} · runs on the server</p>
            {agent.activity && <p className="text-xs text-muted-foreground">{agent.activity}{agent.last_active_label ? ` · ${agent.last_active_label}` : ''}</p>}
            {(agent.model_id || agent.reasoning_effort || agent.crew_name) && <p className="break-words text-xs text-muted-foreground">{[agent.model_id && `Model: ${agent.model_id}`, agent.reasoning_effort && `Effort: ${agent.reasoning_effort}`, agent.crew_name && `Crew: ${agent.crew_name}`].filter(Boolean).join(' · ')}</p>}
          </div>
          <button type="button" className="text-xs text-muted-foreground hover:text-foreground" aria-expanded={statusTab === agent.tab} onClick={() => setStatusTab(statusTab === agent.tab ? '' : agent.tab)}>Status</button>
          <AskButton projectId={projectId} label="Ask AI" prompt={`Help me manage this Crew’s online agent ${JSON.stringify(agent.name)} (tab ${JSON.stringify(agent.tab)}). Find that existing online agent and ask it for its current schedules, webhooks and watchers on the server, including which are on and when they run next. Help me set up or change them when I ask. Do not edit the disabled Mac entries or create a different agent.`} />
        </div>
        {statusTab === agent.tab && <div className="ml-6 space-y-1 text-xs">
          <p className="text-muted-foreground">Existing status only. No message sent.</p>
          {agent.status?.current_task && <p>Current task: {agent.status.current_task}</p>}
          {agent.status?.current_step && <p>Current step: {agent.status.current_step}</p>}
          {agent.status?.last_messages?.map((line, index) => <p key={index}>{line}</p>)}
          {!agent.status?.current_task && !agent.status?.current_step && !agent.status?.last_messages?.length && <p className="text-muted-foreground">No task or message preview available.</p>}
        </div>}
        {agent.automations ? agent.automations.length ? <ul className="ml-6 space-y-1.5 text-xs">
          {agent.automations.map((automation, index) => <li key={`${automation.kind}:${automation.name}:${index}`}>
            <div className="flex flex-wrap items-baseline gap-x-2"><span className="font-medium">{automation.name}</span><span className="text-muted-foreground">{automation.kind} · {automation.on ? 'On' : 'Off'}</span></div>
            <p className="text-muted-foreground">{automation.line}</p>
          </li>)}
        </ul> : <p className="ml-6 text-xs text-muted-foreground">No schedules, webhooks or watchers set up for this online agent.</p> : <p className="ml-6 text-xs text-muted-foreground">{error ? 'Automation details unavailable.' : 'Loading automations from the server…'}</p>}
      </div>)}
    </div>
    <div className="flex flex-wrap items-center gap-2 border-t border-border pt-2.5">
      <AskButton projectId={projectId} prompt={run} label="Run an agent online" />
      {website && <a href={website} target="_blank" rel="noopener noreferrer" className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted">Open on website</a>}
    </div>
  </section>
}
