import { useCallback, useEffect } from 'react'
import { create } from 'zustand'
import { useChatStore } from '../../stores/useChatStore'
import { teamAgentForTab, teamRequest, useTeamTab, type TeamTarget, type TeamTabState } from './teamApi'

// Features (#3210): the agents and history of one piece of work in a Crew. The server keeps them (crew_features.go)
// and decides everything; these screens show what it says and send what the person picked.

export type FeatureAgent = { name: string; chat_key: string; joined?: boolean; person?: string }
export type Feature = { id: string; name: string; state: 'active' | 'done'; link?: string; agents: FeatureAgent[]; open_tasks: number; done_tasks: number; changed_at?: string }
export type FeatureEvent = { id: string; at: string; kind: string; by?: string; text: string; why?: string; proof?: string; task?: string }
export type FeatureTask = { id: string; title: string; state: string; agent?: string; person?: string; last?: string; issue?: string; at?: string }
export type FeatureView = Feature & { now: FeatureTask[]; blocked: FeatureTask[]; waiting_on_a_person: FeatureTask[]; last_done?: FeatureEvent; decisions: FeatureEvent[]; story: FeatureEvent[] }

/** Where this Crew's features are: with the Team on, the Team's (on the server, through this Mac's sign-in when the
 * Crew is on a Mac); with it off, this app's own. The same rule as the Tasks section. */
export function featuresTarget(team: TeamTabState, projectId: string): TeamTarget | undefined {
  const on = !!team.view?.team.on && !!team.target && !team.error
  return on ? team.target : projectId ? { project: projectId } : undefined
}

/** The chat key the server knows this tab by: its own key, or on a Mac whose Crew's Team is online, the Team's key
 * for this Mac's tab ("main" for the Crew's main chat). */
export function featureChatKey(target: TeamTarget | undefined, key: string | undefined, main: boolean) {
  if (!target || !key) return ''
  if (!target.localProject) return key
  return `${target.project}:chat:${main ? 'main' : key.split(':chat:')[1] || 'main'}`
}

type Loaded = { key: string; target?: TeamTarget; features: Feature[]; error: string }
const useFeaturesStore = create<{ loaded: Record<string, Loaded>; set: (project: string, value: Loaded) => void }>(set => ({
  loaded: {},
  set: (project, value) => set(state => ({ loaded: { ...state.loaded, [project]: value } })),
}))
const inFlight = new Map<string, Promise<void>>()
const targetKey = (target?: TeamTarget) => target ? `${target.connection || ''}\x00${target.project}` : ''

export async function featureCall<T>(target: TeamTarget, body: Record<string, unknown>) {
  return teamRequest<T>(target, '/work', 'POST', body)
}

async function refresh(projectId: string, target: TeamTarget) {
  const key = targetKey(target)
  const running = inFlight.get(key)
  if (running) return running
  const job = featureCall<{ features: Feature[] }>(target, { op: 'features' })
    .then(out => useFeaturesStore.getState().set(projectId, { key, target, features: out.features || [], error: '' }))
    .catch(e => useFeaturesStore.getState().set(projectId, { key, target, features: useFeaturesStore.getState().loaded[projectId]?.features || [], error: e instanceof Error ? e.message : 'Features are unavailable' }))
    .finally(() => inFlight.delete(key))
  inFlight.set(key, job)
  return job
}

const changed = 'agentworks-features-changed'
/** Every list of this Crew's features reads again (after a join or a leave). */
export const featuresChanged = () => window.dispatchEvent(new Event(changed))

/** This Crew's active features, shared by the left menu, the top bar and the Work view. Read again every 30 s. */
export function useCrewFeatures(projectId: string) {
  const team = useTeamTab(projectId)
  const target = featuresTarget(team, projectId)
  const key = targetKey(target)
  const loaded = useFeaturesStore(state => state.loaded[projectId])
  useEffect(() => {
    if (!target) return
    const load = () => { void refresh(projectId, target) }
    load()
    const timer = window.setInterval(load, 30_000)
    window.addEventListener(changed, load)
    return () => { window.clearInterval(timer); window.removeEventListener(changed, load) }
    // target is rebuilt each render; its key names it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, key])
  const current = loaded?.key === key ? loaded : undefined
  const features = (current?.features || []).filter(feature => feature.state !== 'done')
  const tabs = useChatStore(state => state.chatTabs)
  /** The key the server knows this tab's agent by. On a Mac whose Crew's Team is on, the Team keeps this Mac's agents
   * under its own keys ("…:chat:mac-<person>-<tab>"), so take it from the Team's row for this tab. */
  const chatKeyFor = useCallback((tabId: string | undefined, main: boolean) => {
    const conversation = tabId ? tabs[tabId]?.metadata?.agentProfileConversationKey : undefined
    const row = target?.localProject ? teamAgentForTab(team, conversation, main, true) : undefined
    return row?.watch_key || featureChatKey(target, conversation, main)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabs, key, team.view?.agents])
  /** The features this tab's agent is in. */
  const forTab = useCallback((tabId: string | undefined, main: boolean) => {
    const chat = chatKeyFor(tabId, main)
    return chat ? features.filter(feature => feature.agents.some(agent => agent.chat_key === chat)) : []
  }, [features, chatKeyFor])
  return { target, features, error: current?.error || '', forTab, chatKeyFor }
}

/** Opens the right panel's Work view ({ projectId }), from the ◈ chip of an agent that is in a feature. */
export const openCrewWorkEvent = 'agentworks:open-crew-work'
