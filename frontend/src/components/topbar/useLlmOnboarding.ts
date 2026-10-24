import { openWorkspaceSettings, closeWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import { workspaceSettingsPage } from '../../utils/workspaceSettingsRoutes'
import { useCallback, useEffect, useState } from 'react'
import { useLLMStore } from '../../stores'
import {
  dismissLLMDiscoveryOnboarding,
  isLLMDiscoveryOnboardingDismissed,
  markLLMDiscoveryOnboardingCleared,
  markLLMDiscoveryOnboardingOpen,
} from '../../utils/onboarding'

const FORCE_LLM_DISCOVERY_ONBOARDING_FOR_TESTING = false

/**
 * Encapsulates the first-run LLM setup behavior that used to live in the
 * sidebar: auto-opening the discovery modal when no model is configured.
 * Returns the modal state and handlers consumed by the global model controls.
 */
export function useLlmOnboarding() {
  const {
    showLLMModal,
    setShowLLMModal,
    savedLLMs,
    defaultsLoaded,
    primaryConfig,
    agentConfig,
    chatPrimaryConfig,
    chatAgentConfig,
    workflowPrimaryConfig,
    workflowAgentConfig,
  } = useLLMStore()
  const [llmOnboardingActive, setLLMOnboardingActive] = useState(false)

  const llmCount = savedLLMs.length

  const hasConfiguredLLM =
    savedLLMs.length > 0 ||
    [primaryConfig, chatPrimaryConfig, workflowPrimaryConfig].some(config => Boolean(config?.provider && config?.model_id?.trim())) ||
    [agentConfig, chatAgentConfig, workflowAgentConfig].some(config => Boolean(config?.primary?.provider && config?.primary?.model_id?.trim()))

  const openLLMConfigModal = useCallback(() => openWorkspaceSettings('providers'), [])

  const openLLMOnboarding = useCallback(() => {
    markLLMDiscoveryOnboardingOpen()
    setLLMOnboardingActive(true)
    openWorkspaceSettings('providers')
  }, [setShowLLMModal])

  const closeLLMConfigurationModal = useCallback(() => {
    if (workspaceSettingsPage() === 'providers') closeWorkspaceSettings()
    else setShowLLMModal(false)
    if (llmOnboardingActive) {
      dismissLLMDiscoveryOnboarding()
      setLLMOnboardingActive(false)
      markLLMDiscoveryOnboardingCleared()
    }
  }, [llmOnboardingActive, setShowLLMModal])

  // Leaving the Providers page through Activity or a workflow also completes
  // the onboarding visit. The terminal itself stays mounted in the page host.
  useEffect(() => {
    if (!showLLMModal && llmOnboardingActive) {
      dismissLLMDiscoveryOnboarding()
      setLLMOnboardingActive(false)
      markLLMDiscoveryOnboardingCleared()
    }
  }, [showLLMModal, llmOnboardingActive])

  // First-run LLM setup opens the same unified Model Library used later.
  useEffect(() => {
    if (FORCE_LLM_DISCOVERY_ONBOARDING_FOR_TESTING) {
      openLLMOnboarding()
      return
    }
    if (!defaultsLoaded || hasConfiguredLLM || workspaceSettingsPage()) return
    if (isLLMDiscoveryOnboardingDismissed()) {
      markLLMDiscoveryOnboardingCleared()
      return
    }
    openLLMOnboarding()
  }, [defaultsLoaded, hasConfiguredLLM, openLLMOnboarding])

  useEffect(() => {
    if (!defaultsLoaded) return
    if (hasConfiguredLLM || isLLMDiscoveryOnboardingDismissed()) {
      markLLMDiscoveryOnboardingCleared()
    }
  }, [defaultsLoaded, hasConfiguredLLM])

  return {
    llmCount,
    showLLMModal,
    openLLMConfigModal,
    closeLLMConfigurationModal,
  }
}
