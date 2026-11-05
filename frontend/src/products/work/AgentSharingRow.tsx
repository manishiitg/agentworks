import { useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { Switch } from '../../components/ui/Switch'
import { fillAgentPurposeDraft } from '../../utils/agentPurposeDraft'
import { teamAgentForTab, teamError, type TeamTabAgent, type TeamTabState, type TeamTarget } from './teamApi'

/** This agent's own chat tab in this app (its person's), found by the Team tab's key; undefined when it is not open. */
export function teamAgentChatTab(state: TeamTabState, projectId: string, key: string) {
  return Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && !item.metadata?.isViewOnly
    && teamAgentForTab(state, item.metadata?.agentProfileConversationKey, item.metadata?.agentProfileIsMainChat !== false, item.metadata?.agentProfileTabCanWrite !== false)?.key === key)
}

type MacShared = { tab: string; audience: 'team' | ''; agentbridge_public?: boolean }

/** A Crew on the server: its Share's selected agents (crew_shared_agents.go), one agent switched, the rest kept. */
async function postShare(target: TeamTarget, body: Record<string, unknown>) {
  if (!target.connection) { await api.post('/api/dashboards', body); return }
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(target.connection)}/api/dashboards`, {
    method: 'POST', headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
  if (!response.ok) { const data = await response.json().catch(() => null); throw new Error(data?.error?.message || data?.error || 'It was not saved. Try again.') }
}

/** The owner's own Mac app: the Mac keeps each agent's Public and pushes the list (crew-sync share_agents). */
async function setMacPublic(target: TeamTarget, tab: string, on: boolean) {
  const endpoint = `/${encodeURIComponent(target.connection!)}/crew-sync/${encodeURIComponent(target.localProject!)}`
  const current = (await desktopRequest<{ agents?: MacShared[] }>(endpoint, 'POST', { action: 'agents' })).agents || []
  const found = current.find(agent => agent.tab === tab)
  // Only the Public flag changes; an agent with no people audience and no Public leaves the list.
  const mine = on || found?.audience ? [{ ...(found || { tab, audience: '' as const }), agentbridge_public: on }] : []
  const next = [...current.filter(agent => agent.tab !== tab), ...mine].map(({ tab, audience, agentbridge_public }) => ({ tab, audience, agentbridge_public: Boolean(agentbridge_public) }))
  await desktopRequest(endpoint, 'POST', { action: 'share_agents', agents: next })
}

/** One agent row's purpose line and AgentBridge line (owner, 5 Nov, approved mockup "Agent Sharing List"): the server
 * writes both and says what the viewer may change; this shows them and sends the choice. */
export function AgentSharingRow({ agent, state, target, projectId, onMacApp }: { agent: TeamTabAgent; state: TeamTabState; target: TeamTarget; projectId: string; onMacApp: boolean }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const bridge = agent.agentbridge
  const askPurpose = () => {
    const tab = teamAgentChatTab(state, projectId, agent.key)
    if (tab) fillAgentPurposeDraft(tab.tabId)
    else useChatStore.getState().addToast(`Open ${agent.name}'s chat, then set its purpose there.`, 'error')
  }
  const setPublic = async (on: boolean) => {
    if (!bridge) return
    // An agent is shared with a purpose: turning it on first asks its chat for one.
    if (on && bridge.needs_purpose) { askPurpose(); setError(`Give ${agent.name} a purpose first, then turn this on.`); return }
    setBusy(true); setError('')
    try {
      if (bridge.share_key) await postShare(target, { workspace: bridge.workspace, action: 'share', shared_agent: bridge.share_key, shared_agent_on: on })
      else await setMacPublic(target, agent.tab, on)
      window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const canSwitch = Boolean(bridge?.can_change && (!bridge.mac_only || onMacApp))
  return <>
    {(agent.purpose || agent.can_set_purpose) && <p className="ml-7 mt-0.5 text-[12.5px] text-muted-foreground">
      {agent.purpose || 'No purpose yet.'}
      {agent.can_set_purpose && <> <button type="button" className="text-primary hover:underline" onClick={askPurpose}>{agent.purpose ? 'Change purpose' : 'Set purpose'}</button></>}
    </p>}
    {bridge && (canSwitch
      ? <label className="ml-7 mt-1 flex items-center gap-2 text-[12.5px]">
        <span className="flex-1 text-muted-foreground">Public on AgentBridge</span>
        <Switch aria-label={`${agent.name}: Public on AgentBridge`} checked={bridge.public} disabled={busy} onCheckedChange={on => void setPublic(on)} />
      </label>
      : <p className="ml-7 mt-1 text-[12.5px] text-muted-foreground">{bridge.text}</p>)}
    {bridge && canSwitch && bridge.public && bridge.line && <p className="ml-7 text-xs text-muted-foreground">{bridge.line}</p>}
    {error && <p role="alert" className="ml-7 text-xs text-destructive">{error}</p>}
  </>
}

/** The owner's Mac app: one line their coding app pastes to reach this Crew's agents. */
export function CopySetupLine({ crewName }: { crewName: string }) {
  const [copied, setCopied] = useState(false)
  const [fallback, setFallback] = useState('')
  const line = `Connect to AgentWorks, then ask my ${JSON.stringify(crewName)} Crew's agent to help me.`
  const copy = async () => {
    try { await navigator.clipboard.writeText(line); setCopied(true); window.setTimeout(() => setCopied(false), 1500) }
    catch { setFallback(line) }
  }
  return <div className="flex flex-wrap items-center gap-2 border-t border-border pt-2 text-xs">
    <button type="button" className="rounded-full border border-border px-2.5 py-0.5 hover:bg-muted" onClick={() => void copy()}>{copied ? 'Copied' : 'Copy setup line'}</button>
    <span className="text-muted-foreground">for your coding app to reach these agents</span>
    {fallback && <p className="w-full text-muted-foreground">Copy this into your coding app: {fallback}</p>}
  </div>
}
