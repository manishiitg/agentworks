/** Rotating hints back off after 5, 10, 20, 40, 80, then 160 input blurs between hints. */
export function nextComposerHint(owner: string, hints: string[]): string | null {
  if (!owner || !hints.length) return null
  const key = `agentworks.composer-hints:${owner}`
  try {
    let saved: { blurs?: number; tip?: number; interval?: number } = {}
    try { saved = JSON.parse(localStorage.getItem(key) || '{}') || {} } catch { /* Start the counter for this person. */ }
    const blurs = (Number.isSafeInteger(saved.blurs) && saved.blurs! >= 0 ? saved.blurs! : 0) + 1
    const tip = Number.isSafeInteger(saved.tip) && saved.tip! >= 0 ? saved.tip! : 0
    const interval = Number.isSafeInteger(saved.interval) && saved.interval! >= 5 && saved.interval! <= 160 ? saved.interval! : 5
    const show = blurs >= interval
    localStorage.setItem(key, JSON.stringify({ blurs: show ? 0 : blurs, tip: show ? (tip + 1) % hints.length : tip, interval: show ? Math.min(interval * 2, 160) : interval }))
    return show ? hints[tip % hints.length] : null
  } catch { return null }
}
