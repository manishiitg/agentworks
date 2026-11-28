import api from '../../../services/api'
import { folderConnection } from '../../../utils/remoteSessions'
import { randomUUID } from '../../../utils/randomUUID'

/** One button of a bot: a project function. "ask" (its chat) is always first. */
export type BotFunction = { name: string; description?: string; input_schema?: { properties?: Record<string, { type?: string; description?: string }>; required?: string[] } }

/** A bot as GET /api/bots lists it (#4545). The server decides who sees which bot. */
export type Bot = {
  id: string; name: string; purpose?: string; icon?: string
  project_id: string; project_title: string; workspace: string; maker_name: string
  mine?: boolean; functions: BotFunction[]
  everyone?: boolean; people?: string[]; teams?: string[]
}

/** The maker's card for one of their agents (GET /api/bots/agent). */
export type BotState = {
  on: boolean; agent: string; name: string; purpose?: string; icon?: string
  everyone: boolean; people: { user_id: string; name: string }[]; teams: string[]; functions: string[]
  available_functions: BotFunction[]; team_choices: { team_id: string; name: string }[]
}

export type BotChange = Partial<{ on: boolean; icon: string; name: string; purpose: string; everyone: boolean; people: string[]; teams: string[]; functions: string[] }>

export async function listBots(): Promise<Bot[]> {
  const { data } = await api.get<{ bots?: Bot[] }>('/api/bots')
  return data.bots ?? []
}

export async function readBotState(workspace: string, agent: string): Promise<BotState> {
  const { data } = await api.get<BotState>('/api/bots/agent', { params: { workspace, agent }, connection: folderConnection(workspace) })
  return data
}

export async function saveBot(workspace: string, agent: string, change: BotChange): Promise<BotState> {
  const { data } = await api.put<BotState>('/api/bots/agent', { workspace, agent, ...change }, { connection: folderConnection(workspace) })
  return data
}

export type BotCallResult = { call_id?: string; status?: string; result?: unknown; error?: string; next?: string }

export async function callBot(bot: string, fn: string, args: Record<string, unknown>): Promise<BotCallResult> {
  const { data } = await api.post<BotCallResult>('/api/bots/call', { bot, function: fn, args, submission_id: randomUUID() })
  return data
}

/** The server's refusal text, for a plain message. */
export function botError(reason: unknown, fallback: string): string {
  const message = (reason as { response?: { data?: { error?: { message?: string }; message?: string } } })?.response?.data
  return message?.error?.message || message?.message || fallback
}
