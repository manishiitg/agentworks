import type { ChatTab } from '../stores/useChatStore'

// Every Crew, Code or workflow opens in the chat view; the terminal is a
// choice for the moment and is not remembered (user, 2026-09-29). Browsers
// that stored a remembered view under the old key have it cleared once.
const RETIRED_STORAGE_KEY = 'agentworks.placeViewMode.v1'
try {
  globalThis.localStorage?.removeItem(RETIRED_STORAGE_KEY)
} catch {
  // Storage can be unavailable (private window); nothing to clear then.
}

/** The place a tab belongs to: a Crew or Code project, or a workflow. */
export function placeViewKey(tab?: Pick<ChatTab, 'metadata'> | null): string | null {
  const meta = tab?.metadata
  if (!meta) return null
  const profile = meta.agentProfileId?.trim()
  const project = meta.agentProfileProjectId?.trim()
  if (profile && project) return `${profile}:${project}`
  const preset = meta.presetQueryId?.trim()
  if (preset) return `workflow:${preset}`
  return null
}
