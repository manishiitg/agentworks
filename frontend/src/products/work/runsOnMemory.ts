import { getWorkspaceScopedStorageKey } from '../../stores/useWorkspaceConnectionStore'

// The CLI picked for the last new Crew, Code or workflow, so the next one starts with it.
const legacyChoiceKey = (profileId: string) => `agentworks.runsOn.${profileId}`
const lastChoiceKey = (profileId: string) => getWorkspaceScopedStorageKey(legacyChoiceKey(profileId))

export const readLastRunsOn = (profileId: string) => {
  try {
    const scopedKey = lastChoiceKey(profileId)
    const saved = window.localStorage.getItem(scopedKey)
    if (saved !== null) return saved
    const legacy = window.localStorage.getItem(legacyChoiceKey(profileId)) || ''
    if (legacy) window.localStorage.setItem(scopedKey, legacy)
    return legacy
  } catch { return '' }
}

/** Remembers the provider picked for a new project, so the next one starts with it. */
export function rememberRunsOn(profileId: string, provider: string) {
  try { window.localStorage.setItem(lastChoiceKey(profileId), provider) } catch { /* private window */ }
}
