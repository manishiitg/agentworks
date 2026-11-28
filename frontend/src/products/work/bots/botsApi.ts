import api from '../../../services/api'
import type { AvatarFields } from '../../../components/ui/Avatar'
import { folderConnection, remoteProjectConnection } from '../../../utils/remoteSessions'
import { isLocalDeployment } from '../../productSurfaceConfig'
import { connectedLocations, useConnectedWorkStore } from '../../../stores/useConnectedWorkStore'

/** A server this Mac is signed in to (its saved sign-in's connection id and name). */
export type BotServer = { id: string; name: string }

/** Bots live on servers (#4545). The Mac app reaches every server it is signed in to and connected to, through each
 * saved sign-in; the website is the server itself (undefined). */
export function botsServers(): BotServer[] | undefined {
  if (!isLocalDeployment()) return undefined
  const state = useConnectedWorkStore.getState()
  return connectedLocations(state)
    .filter(connection => connection.id !== 'local' && state.inventory[connection.id]?.status === 'connected')
    .map(connection => ({ id: connection.id, name: connection.name }))
}

/** Where a project's bot requests go: its own server. On a Mac, a server project goes to that server through the
 * saved sign-in, and a project on this Mac to this Mac, which reaches the servers' bots itself. */
const projectConnection = (projectId: string) => isLocalDeployment() ? remoteProjectConnection(projectId) : undefined

/** A function of a bot: other agents call it by name ("ask", its chat, is always first). */
export type BotFunction = { name: string; description?: string; input_schema?: { properties?: Record<string, { type?: string; description?: string }>; required?: string[] } }

/** A bot as GET /api/bots lists it (#4545). The server decides who sees which bot. */
export type Bot = AvatarFields & {
  /** On a Mac, a project's bot: the sign-in of its server (its photo is read through it). */
  connection?: string
  id: string; name: string; purpose?: string; icon?: string
  project_id: string; project_title: string; workspace: string; maker_name: string
  mine?: boolean; functions: BotFunction[]
  everyone?: boolean; people?: string[]; teams?: string[]
  /** Your own projects it was added to. */
  in_projects?: BotProject[]
  /** Your projects it can be added to: on its server (owner or co-owner), and on this Mac. */
  add_targets?: BotProject[]
  /** On a Mac: the server it lives on. */
  server?: BotServer
}

/** One of your projects where you can add a bot: on the server (owner or co-owner), or on this Mac (mac). */
export type BotProject = { project_id: string; title: string; mac?: boolean }

/** Bots changed (made, added or removed): the Bots page, agent list and right menu read them again. */
export const BOTS_CHANGED_EVENT = 'agentworks:bots-changed'
export const botsChanged = () => window.dispatchEvent(new Event(BOTS_CHANGED_EVENT))

/** The maker's card for one of their agents (GET /api/bots/agent). */
export type BotState = AvatarFields & {
  /** The icons a bot's picture may use. */
  avatar_icons?: string[]
  on: boolean; agent: string; name: string; purpose?: string; icon?: string
  everyone: boolean; people: { user_id: string; name: string }[]; teams: string[]; functions: string[]
  available_functions: BotFunction[]; team_choices: { team_id: string; name: string }[]
}

/** A picture change: a photo (base64), or an icon and colour; remove goes back to the emoji. */
export type BotAvatarChange = { operation: 'set' | 'remove'; icon?: string; color?: string; image_base64?: string }
export type BotChange = Partial<{ avatar: BotAvatarChange; on: boolean; icon: string; name: string; purpose: string; everyone: boolean; people: string[]; teams: string[]; functions: string[] }>

type ServerBots = { bots?: Bot[]; add_targets?: BotProject[] }
type MacProject = { project_id: string; title: string; added_bots?: string[]; bot_servers?: Record<string, string> }

/** The bots shared with you, each with the projects you can add it to. On a Mac: from every server you are signed in
 * to, each bot tagged with its server, and this Mac's projects can add them too (they keep them here). */
export async function listBots(): Promise<Bot[]> {
  const servers = botsServers()
  if (!servers) {
    const { data } = await api.get<ServerBots>('/api/bots')
    return (data.bots ?? []).map(bot => ({ ...bot, add_targets: data.add_targets ?? [] }))
  }
  const [lists, local] = await Promise.all([
    Promise.allSettled(servers.map(server => api.get<ServerBots>('/api/bots', { connection: server.id }).then(({ data }) => ({ server, data })))),
    api.get<{ projects?: MacProject[] }>('/api/bots/mac-projects').then(r => r.data.projects ?? []).catch(() => [] as MacProject[]),
  ])
  const loaded = lists.flatMap(item => item.status === 'fulfilled' ? [item.value] : [])
  if (servers.length && !loaded.length) throw (lists.find(item => item.status === 'rejected') as PromiseRejectedResult).reason
  const mac = local.map(project => ({ project_id: project.project_id, title: project.title, mac: true }))
  return loaded.flatMap(({ server, data }) => (data.bots ?? []).map(bot => {
    const here = local.filter(project => project.added_bots?.includes(bot.id) && project.bot_servers?.[bot.id] === server.id)
      .map(project => ({ project_id: project.project_id, title: project.title, mac: true }))
    return { ...bot, server, add_targets: [...(data.add_targets ?? []), ...mac], in_projects: [...(bot.in_projects ?? []), ...here] }
  }))
}

export type ProjectBots = { bots: Bot[]; /** Ids of added bots no longer shared with you. */ unavailable: string[] }

/** The bots added to a project, as you may use them; unavailable counts those no longer shared with you. */
export async function readProjectBots(projectId: string): Promise<ProjectBots> {
  const { data } = await api.get<{ bots?: Bot[]; unavailable?: string[] }>('/api/bots/project', { params: { project_id: projectId }, connection: projectConnection(projectId) })
  return { bots: data.bots ?? [], unavailable: data.unavailable ?? [] }
}

/** Add a bot to one of your projects, or take it out. A project on this Mac also keeps which server the bot is on. */
export async function setProjectBot(projectId: string, bot: string, add: boolean, server?: BotServer): Promise<ProjectBots> {
  const connection = projectConnection(projectId)
  const body = { project_id: projectId, bot, add, ...(isLocalDeployment() && !connection && server ? { server: server.id } : {}) }
  const { data } = await api.post<{ bots?: Bot[]; unavailable?: string[] }>('/api/bots/project', body, { connection })
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

/** The server's refusal text, for a plain message. */
export function botError(reason: unknown, fallback: string): string {
  const message = (reason as { response?: { data?: { error?: { message?: string }; message?: string } } })?.response?.data
  return message?.error?.message || message?.message || fallback
}
