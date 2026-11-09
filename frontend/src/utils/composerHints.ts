/** One rotating hint per 50 input blurs, remembered for this person on this device. */
export function nextComposerHint(owner: string, hints: string[]): string | null {
  if (!owner || !hints.length) return null
  const key = `agentworks.composer-hints:${owner}`
  try {
    let saved: { blurs?: number; tip?: number } = {}
    try { saved = JSON.parse(localStorage.getItem(key) || '{}') || {} } catch { /* Start the counter for this person. */ }
    const blurs = (Number.isSafeInteger(saved.blurs) && saved.blurs! >= 0 ? saved.blurs! : 0) + 1
    const tip = Number.isSafeInteger(saved.tip) && saved.tip! >= 0 ? saved.tip! : 0
    const show = blurs >= 50
    localStorage.setItem(key, JSON.stringify({ blurs: show ? 0 : blurs, tip: show ? (tip + 1) % hints.length : tip }))
    return show ? hints[tip % hints.length] : null
  } catch { return null }
}
