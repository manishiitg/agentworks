// Our own plain, friendly words for "the agent is busy" (display only). The server's real status always wins over them.
export const WORKING_WORDS = [
  'Thinking', 'Pondering', 'Brewing', 'Sketching', 'Tinkering', 'Weaving', 'Gathering', 'Polishing', 'Mulling',
  'Stitching', 'Shaping', 'Exploring', 'Untangling', 'Assembling', 'Crafting', 'Sorting', 'Piecing together',
  'Connecting dots', 'Warming up', 'Digging in', 'Mapping it out', 'Fine-tuning', 'Lining things up', 'Checking in',
  'Cooking up ideas', 'Working it out', 'Putting it together', 'Taking a look', 'Getting there', 'Making progress',
] as const

export const WORKING_WORD_INTERVAL_MS = 3500
export const WORKING_ACCESSIBLE_LABEL = 'Working…'

// Statuses that only say "busy" carry no information, so a playful word replaces them.
const GENERIC_STATUSES = new Set(['', 'working', 'working…', 'working...', 'running', 'thinking'])

// What to show: a real status from the server (a tool running, "Waiting for background work…", a step name) wins over
// the playful word. Pure, so it can be tested without rendering.
export function resolveWorkingLabel(realStatus: string | undefined, playfulWord: string): { text: string; real: boolean } {
  const real = (realStatus ?? '').trim()
  if (real && !GENERIC_STATUSES.has(real.toLowerCase())) return { text: real, real: true }
  return { text: `${playfulWord}…`, real: false }
}

// 17s, then 1m 05s, then 1h 02m.
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  if (total < 60) return `${total}s`
  const minutes = Math.floor(total / 60)
  if (minutes < 60) return `${minutes}m ${String(total % 60).padStart(2, '0')}s`
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, '0')}m`
}
