// Which product features this person has already used (per browser). A tip
// for a feature they use is never shown (owner, 2026-10-07: rare, intelligent).
const KEY = 'agentworks.features-used'

export type UsedFeature = 'quick-switcher' | 'panel-switcher' | 'toolbar-hide' | 'chat-tab-keys' | 'chat-tab-new'

function read(): Record<string, number> {
  try { return JSON.parse(localStorage.getItem(KEY) || '{}') as Record<string, number> } catch { return {} }
}

export function markFeatureUsed(feature: UsedFeature) {
  try {
    const used = read()
    if (used[feature]) return
    used[feature] = Date.now()
    localStorage.setItem(KEY, JSON.stringify(used))
  } catch { /* per-viewer convenience only */ }
}

export function featureUsed(feature: UsedFeature): boolean {
  return Boolean(read()[feature])
}
