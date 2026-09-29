import type { ChatTab, EventViewMode } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'

// Chat or terminal, remembered per Crew, Code or workflow (and per person on
// this browser). Only the person's own toggle records it; automatic switches
// (a terminal that is unavailable, navigation) never do. Browser-local: a
// convenience, never needed for correctness.

const STORAGE_KEY = 'agentworks.placeViewMode.v1'

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

function scopedKey(place: string): string {
  const user = useAuthStore.getState().user?.id || 'local'
  return `${user}|${place}`
}

function readAll(): Record<string, EventViewMode> {
  try {
    const raw = globalThis.localStorage?.getItem(STORAGE_KEY)
    const parsed = raw ? JSON.parse(raw) : {}
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

export function rememberedPlaceViewMode(tab?: Pick<ChatTab, 'metadata'> | null): EventViewMode | null {
  const place = placeViewKey(tab)
  if (!place) return null
  const mode = readAll()[scopedKey(place)]
  return mode === 'terminal' || mode === 'formatted' ? mode : null
}

export function rememberPlaceViewMode(tab: Pick<ChatTab, 'metadata'> | null | undefined, mode: EventViewMode): void {
  const place = placeViewKey(tab)
  if (!place) return
  try {
    const all = readAll()
    all[scopedKey(place)] = mode
    globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(all))
  } catch {
    // Storage unavailable: the choice just isn't remembered.
  }
}
