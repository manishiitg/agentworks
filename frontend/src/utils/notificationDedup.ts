/**
 * Notification deduplication utility.
 * Persists to localStorage so an answered request stays closed on page refresh.
 */

const SUBMITTED_FEEDBACK_KEY = 'mcp_submitted_feedback'
export const HUMAN_FEEDBACK_SUBMITTED_EVENT = 'agentworks:human-feedback-submitted'
// Entries older than 1 hour are purged
const MAX_AGE_MS = 60 * 60 * 1000

interface TimestampedEntry<T> {
  value: T
  ts: number
}

function loadMap<T>(key: string): Map<string, TimestampedEntry<T>> {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return new Map()
    const entries: [string, TimestampedEntry<T>][] = JSON.parse(raw)
    const now = Date.now()
    // Filter out expired entries
    return new Map(entries.filter(([, entry]) => now - entry.ts < MAX_AGE_MS))
  } catch {
    return new Map()
  }
}

function saveMap<T>(key: string, map: Map<string, TimestampedEntry<T>>): void {
  try {
    localStorage.setItem(key, JSON.stringify(Array.from(map.entries())))
  } catch {
    // localStorage full or unavailable — ignore
  }
}

// --- Submitted feedback marker (persisted across page reloads) ---
//
// Human feedback can contain OTPs or other private input. Persist only the fact
// that the request was answered so a replayed event stays closed; never persist
// the response itself.

export function hasSubmittedFeedback(requestId: string): boolean {
  const map = loadMap<unknown>(SUBMITTED_FEEDBACK_KEY)
  const entry = map.get(requestId)
  if (!entry) return false

  // Sanitize entries written by older builds, which cached the raw response.
  if (entry.value !== true) {
    map.set(requestId, { value: true, ts: entry.ts })
    saveMap(SUBMITTED_FEEDBACK_KEY, map)
  }
  return true
}

export function markFeedbackSubmitted(requestId: string): void {
  const map = loadMap<boolean>(SUBMITTED_FEEDBACK_KEY)
  map.set(requestId, { value: true, ts: Date.now() })
  saveMap(SUBMITTED_FEEDBACK_KEY, map)
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(HUMAN_FEEDBACK_SUBMITTED_EVENT, {
      detail: { requestId },
    }))
  }
}
