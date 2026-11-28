import api from '../../../services/api'
import { folderConnection, remoteProjectConnection } from '../../../utils/remoteSessions'
import { randomUUID } from '../../../utils/randomUUID'
import { isLocalDeployment } from '../../productSurfaceConfig'
import { connectedLocations, useConnectedWorkStore } from '../../../stores/useConnectedWorkStore'

/** Bots live on the server (#4545). The Mac app reaches the server it is signed in to (the first connected one) through
 * its saved sign-in; the website is the server itself (undefined). */
export function botsServer(): { id: string; name: string } | null | undefined {
  if (!isLocalDeployment()) return undefined
  const state = useConnectedWorkStore.getState()
  const inventory = state.inventory
  const server = connectedLocations(state).find(connection => connection.id !== 'local' && inventory[connection.id]?.status === 'connected')
  return server ? { id: server.id, name: server.name } : null
}

/** Where a project's bot requests go: its own server. A Mac-only project has none (undefined with ok=false). */
function projectServer(projectId: string): { ok: boolean; connection?: string } {
  if (!isLocalDeployment()) return { ok: true }
  const connection = remoteProjectConnection(projectId)
  return connection ? { ok: true, connection } : { ok: false }
}
const serverConnection = () => botsServer()?.id || undefined
/** On a Mac, bots come only from a server it is signed in to. */
export const projectCanHaveBots = (projectId: string) => projectServer(projectId).ok

/** One button of a bot: a project function. "ask" (its chat) is always first. */
export type BotFunction = { name: string; description?: string; input_schema?: { properties?: Record<string, { type?: string; description?: string }>; required?: string[] } }

/** A bot as GET /api/bots lists it (#4545). The server decides who sees which bot. */
export type Bot = {
  id: string; name: string; purpose?: string; icon?: string
  project_id: string; project_title: string; workspace: string; maker_name: string
  mine?: boolean; functions: BotFunction[]
  everyone?: boolean; people?: string[]; teams?: string[]
  /** Your own projects it was added to. */
  in_projects?: BotProject[]
}

/** One of your projects on the server (owner or co-owner): where you can add a bot. */
export type BotProject = { project_id: string; title: string }

/** Bots changed (made, added or removed): the Bots page, agent list and right menu read them again. */
export const BOTS_CHANGED_EVENT = 'agentworks:bots-changed'
export const botsChanged = () => window.dispatchEvent(new Event(BOTS_CHANGED_EVENT))

/** The maker's card for one of their agents (GET /api/bots/agent). */
export type BotState = {
  on: boolean; agent: string; name: string; purpose?: string; icon?: string
  everyone: boolean; people: { user_id: string; name: string }[]; teams: string[]; functions: string[]
  available_functions: BotFunction[]; team_choices: { team_id: string; name: string }[]
}

export type BotChange = Partial<{ on: boolean; icon: string; name: string; purpose: string; everyone: boolean; people: string[]; teams: string[]; functions: string[] }>

export async function listBots(): Promise<{ bots: Bot[]; addTargets: BotProject[] }> {
  const { data } = await api.get<{ bots?: Bot[]; add_targets?: BotProject[] }>('/api/bots', { connection: serverConnection() })
  return { bots: data.bots ?? [], addTargets: data.add_targets ?? [] }
}

export type ProjectBots = { bots: Bot[]; /** Ids of added bots no longer shared with you. */ unavailable: string[] }

/** The bots added to a project, as you may use them; unavailable counts those no longer shared with you. */
export async function readProjectBots(projectId: string): Promise<ProjectBots> {
  const { data } = await api.get<{ bots?: Bot[]; unavailable?: string[] }>('/api/bots/project', { params: { project_id: projectId }, connection: projectServer(projectId).connection })
  return { bots: data.bots ?? [], unavailable: data.unavailable ?? [] }
}

export async function setProjectBot(projectId: string, bot: string, add: boolean): Promise<ProjectBots> {
  const { data } = await api.post<{ bots?: Bot[]; unavailable?: string[] }>('/api/bots/project', { project_id: projectId, bot, add }, { connection: projectServer(projectId).connection ?? serverConnection() })
  botsChanged()
  return { bots: data.bots ?? [], unavailable: data.unavailable ?? [] }
}

export async function readBotState(workspace: string, agent: string): Promise<BotState> {
  const { data } = await api.get<BotState>('/api/bots/agent', { params: { workspace, agent }, connection: folderConnection(workspace) })
  return data
}

export async function saveBot(workspace: string, agent: string, change: BotChange): Promise<BotState> {
  const { data } = await api.put<BotState>('/api/bots/agent', { workspace, agent, ...change }, { connection: folderConnection(workspace) })
  botsChanged()
  return data
}

export type BotCallResult = { call_id?: string; status?: string; result?: unknown; error?: string; next?: string }

export async function callBot(bot: string, fn: string, args: Record<string, unknown>): Promise<BotCallResult> {
  const { data } = await api.post<BotCallResult>('/api/bots/call', { bot, function: fn, args, submission_id: randomUUID() }, { connection: serverConnection() })
  return data
}

/** The server's refusal text, for a plain message. */
export function botError(reason: unknown, fallback: string): string {
  const message = (reason as { response?: { data?: { error?: { message?: string }; message?: string } } })?.response?.data
  return message?.error?.message || message?.message || fallback
}
