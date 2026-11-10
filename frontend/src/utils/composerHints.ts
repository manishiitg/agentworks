import { usedFeatures } from './featureUsage'

type ComposerHint = { id: string; text: string; feature?: string }
const keyFor = (owner: string) => `agentworks.composer-hints:${owner}`
type HintUse = { used?: Record<string, boolean>; blurs?: number; tip?: number; shownAt?: number }
function read(owner: string): HintUse {
  try { return JSON.parse(localStorage.getItem(keyFor(owner)) || '{}') || {} } catch { return {} }
}
export function recordComposerShortcut(owner: string, id: string) {
  if (!owner) return
  try { const saved = read(owner); localStorage.setItem(keyFor(owner), JSON.stringify({ ...saved, used: { ...saved.used, [id]: true } })) } catch { /* Optional local convenience. */ }
}

// Teach unfamiliar shortcuts, not keys the person already uses. Keep the idle
// composer quiet: at most one hint a day, after twenty blurs, only when empty.
export function nextComposerHint(owner: string, hints: ComposerHint[]): string | null {
  if (!owner) return null
  try {
    const saved = read(owner)
    const features = new Set(usedFeatures())
    const remaining = hints.filter(hint => !saved.used?.[hint.id] && (!hint.feature || !features.has(hint.feature)))
    if (!remaining.length) return null
    const blurs = (Number.isSafeInteger(saved.blurs) && saved.blurs! >= 0 ? saved.blurs! : 0) + 1
    const tip = Number.isSafeInteger(saved.tip) && saved.tip! >= 0 ? saved.tip! : 0
    const show = blurs >= 20 && (!saved.shownAt || Date.now() - saved.shownAt >= 24 * 60 * 60 * 1000)
    localStorage.setItem(keyFor(owner), JSON.stringify({ ...saved, blurs: show ? 0 : blurs, tip: show ? tip + 1 : tip, shownAt: show ? Date.now() : saved.shownAt }))
    return show ? remaining[tip % remaining.length].text : null
  } catch { return null }
}
