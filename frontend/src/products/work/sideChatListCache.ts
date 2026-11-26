import { agentApi } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

const FRESH_MS = 3_000
type Entry = { at: number; rows: AgentProfileSideChat[] }

let cacheIdentity = -1
const entries = new Map<string, Entry>()
const inFlight = new Map<string, Promise<AgentProfileSideChat[]>>()

function ensureIdentity(identity: number) {
  if (cacheIdentity === identity) return
  cacheIdentity = identity
  entries.clear()
  inFlight.clear()
}

/**
 * Deduplicates concurrent and back-to-back side-chat list reads across the
 * sidebar and WorkSurface within the current chat identity. Always returns a
 * shallow copy so callers cannot mutate cached rows.
 */
export async function listProjectSideChats(
  profileId: string,
  projectId: string,
  withMain = false,
  force = false,
): Promise<AgentProfileSideChat[]> {
  const identity = captureChatIdentity()
  ensureIdentity(identity)
  const key = `${profileId}:${projectId}:${withMain ? 1 : 0}`
  if (!force) {
    const cached = entries.get(key)
    if (cached && Date.now() - cached.at < FRESH_MS) return [...cached.rows]
    const pending = inFlight.get(key)
    if (pending) return [...await pending]
  }
  const request = agentApi.listAgentProfileSideChats(profileId, projectId, withMain).then(rows => {
    const list = Array.isArray(rows) ? rows : []
    if (isChatIdentityCurrent(identity) && cacheIdentity === identity) {
      entries.set(key, { at: Date.now(), rows: list })
    }
    return list
  }).finally(() => {
    if (inFlight.get(key) === request) inFlight.delete(key)
  })
  inFlight.set(key, request)
  return [...await request]
}

export function clearSideChatListCache() {
  cacheIdentity = -1
  entries.clear()
  inFlight.clear()
}
