export const LLM_DISCOVERY_ONBOARDING_DISMISSED_KEY = 'llm_discovery_onboarding_dismissed'
export type WalkthroughSurface = 'overview' | 'empty-automation' | 'automation' | 'empty-crew' | 'crew' | 'empty-code' | 'code'
const WALKTHROUGH_DISMISSED_KEYS: Record<WalkthroughSurface, string> = {
  overview: 'agentworks_overview_walkthrough_v3_dismissed',
  'empty-automation': 'agentworks_empty_automation_walkthrough_v3_dismissed',
  automation: 'agentworks_automation_walkthrough_v3_dismissed',
  'empty-crew': 'agentworks_empty_crew_walkthrough_v3_dismissed',
  crew: 'agentworks_crew_walkthrough_v3_dismissed',
  'empty-code': 'agentworks_empty_code_walkthrough_v1_dismissed',
  code: 'agentworks_code_walkthrough_v1_dismissed',
}

export const LLM_DISCOVERY_ONBOARDING_OPENED_EVENT = 'llm-discovery-onboarding-opened'
export const LLM_DISCOVERY_ONBOARDING_CLEARED_EVENT = 'llm-discovery-onboarding-cleared'

type LLMDiscoveryOnboardingState = 'pending' | 'open' | 'cleared'

const getWindowState = (): LLMDiscoveryOnboardingState | undefined => {
  if (typeof window === 'undefined') return undefined
  return (window as Window & { __llmDiscoveryOnboardingState?: LLMDiscoveryOnboardingState }).__llmDiscoveryOnboardingState
}

const setWindowState = (state: LLMDiscoveryOnboardingState) => {
  if (typeof window === 'undefined') return
  ;(window as Window & { __llmDiscoveryOnboardingState?: LLMDiscoveryOnboardingState }).__llmDiscoveryOnboardingState = state
}

const getStorageValue = (key: string): string | null => {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

const setStorageValue = (key: string, value: string) => {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // Storage may be unavailable; in-memory onboarding state still works.
  }
}

export const isLLMDiscoveryOnboardingDismissed = () =>
  getStorageValue(LLM_DISCOVERY_ONBOARDING_DISMISSED_KEY) === 'true'

export const dismissLLMDiscoveryOnboarding = () => {
  setStorageValue(LLM_DISCOVERY_ONBOARDING_DISMISSED_KEY, 'true')
}

export const isWorkflowWalkthroughDismissed = (surface: WalkthroughSurface) =>
  getStorageValue(WALKTHROUGH_DISMISSED_KEYS[surface]) === 'true'

export const dismissWorkflowWalkthrough = (surface: WalkthroughSurface) => {
  setStorageValue(WALKTHROUGH_DISMISSED_KEYS[surface], 'true')
}

export const getLLMDiscoveryOnboardingState = (): LLMDiscoveryOnboardingState => {
  const state = getWindowState()
  if (state) return state
  return isLLMDiscoveryOnboardingDismissed() ? 'cleared' : 'pending'
}

export const markLLMDiscoveryOnboardingOpen = () => {
  setWindowState('open')
  if (typeof window === 'undefined') return
  window.dispatchEvent(new Event(LLM_DISCOVERY_ONBOARDING_OPENED_EVENT))
}

export const markLLMDiscoveryOnboardingCleared = () => {
  setWindowState('cleared')
  if (typeof window === 'undefined') return
  window.dispatchEvent(new Event(LLM_DISCOVERY_ONBOARDING_CLEARED_EVENT))
}
