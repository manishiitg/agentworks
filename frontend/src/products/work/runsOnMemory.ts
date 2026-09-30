// The CLI picked for the last new Crew, Code or workflow, so the next one starts with it.
const lastChoiceKey = (profileId: string) => `agentworks.runsOn.${profileId}`

export const readLastRunsOn = (profileId: string) => {
  try { return window.localStorage.getItem(lastChoiceKey(profileId)) || '' } catch { return '' }
}

/** Remembers the provider picked for a new project, so the next one starts with it. */
export function rememberRunsOn(profileId: string, provider: string) {
  try { window.localStorage.setItem(lastChoiceKey(profileId), provider) } catch { /* private window */ }
}
