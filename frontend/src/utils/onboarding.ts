export const LLM_DISCOVERY_ONBOARDING_DISMISSED_KEY = 'llm_discovery_onboarding_dismissed'
export type WalkthroughSurface = 'overview' | 'empty-automation' | 'automation' | 'empty-crew' | 'crew' | 'empty-code' | 'code' | 'providers'
const WALKTHROUGH_DISMISSED_KEYS: Record<WalkthroughSurface, string> = {
  overview: 'agentworks_overview_walkthrough_v3_dismissed',
  'empty-automation': 'agentworks_empty_automation_walkthrough_v3_dismissed',
  automation: 'agentworks_automation_walkthrough_v3_dismissed',
  'empty-crew': 'agentworks_empty_crew_walkthrough_v3_dismissed',
  crew: 'agentworks_crew_walkthrough_v3_dismissed',
  'empty-code': 'agentworks_empty_code_walkthrough_v1_dismissed',
  code: 'agentworks_code_walkthrough_v1_dismissed',
  providers: 'agentworks_providers_walkthrough_v1_dismissed',
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

const rememberedGuides = new Set<string>()

export const isGuideRemembered = (key: string) => {
  if (key.startsWith('agentworks_tip_') && rememberedGuides.has(key)) return true
  if (typeof window !== 'undefined') {
    try {
      if (window.electronAPI?.isWalkthroughDismissed?.(key)) return true
    } catch {
      // Fall back to the renderer preference if the desktop bridge is unavailable.
    }
  }
  if (getStorageValue(key) !== 'true') return false
  // Migrate an existing dismissal from this origin into the desktop profile.
  rememberGuide(key)
  return true
}

export const rememberGuide = (key: string) => {
  rememberedGuides.add(key)
  setStorageValue(key, 'true')
  if (typeof window !== 'undefined') {
    try {
      window.electronAPI?.dismissWalkthrough?.(key)
    } catch {
      // Browser clients and older desktop versions keep using localStorage.
    }
  }
}

export const isWorkflowWalkthroughDismissed = (surface: WalkthroughSurface) => isGuideRemembered(WALKTHROUGH_DISMISSED_KEYS[surface])
export const dismissWorkflowWalkthrough = (surface: WalkthroughSurface) => rememberGuide(WALKTHROUGH_DISMISSED_KEYS[surface])

export const contextualGuideKey = (surface: 'agentworks' | 'crew' | 'code' | 'providers', topic: string) => {
  const stableTopic = topic.startsWith('Schedules for ') ? 'Schedules' : topic
  const slug = stableTopic.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '').slice(0, 80)
  return `agentworks_tip_${surface}_${slug || 'workspace'}_v1_dismissed`
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
