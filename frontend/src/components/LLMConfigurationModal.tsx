import { useState, useEffect, useCallback, useMemo } from 'react'
import { X, Settings, Lock } from 'lucide-react'
import { Button } from './ui/Button'
import { TooltipProvider } from './ui/tooltip'
import { useLLMStore, useAppStore } from '../stores'
import type { AgentLLMConfiguration, LLMProvider } from '../services/api-types'
import { CodingAgentSection } from './llm/CodingAgentSection'
import type { ProviderManifestEntry } from '../services/llm-config-api'
import { LibraryTab } from './llm/LibraryTab'
import ModalPortal from './ui/ModalPortal'
import { useCanWriteWorkflow } from '../hooks/useCanWriteWorkflow'
import { effectiveLLMUnderLock } from '../utils/effectiveLLM'
import { installedCodingProviders } from '../utils/providerCatalogFilter'

interface LLMConfigurationModalProps {
  isOpen: boolean
  onClose: () => void
}

// Pi CLI routes through several distinct model backends (Gemini, OpenRouter,
// Z.AI, ...); each gets its own sidebar tab under a synthetic id rather than
// exposing "Pi CLI" as one opaque option. The manifest still has exactly one
// 'pi-cli' provider entry -- these ids exist only in the frontend tab list.
type PiCliGroupTab = `pi-cli::${string}`

type TabType = 'library' | LLMProvider | PiCliGroupTab

const piCliGroupTabId = (group: string): PiCliGroupTab => `pi-cli::${group}`

const CODING_AGENT_PROVIDER_ORDER = ['claude-code', 'codex-cli', 'cursor-cli', 'pi-cli', 'muse-cli', 'agy-cli']
const CODING_AGENT_PROVIDER_RANK = new Map<string, number>(
  CODING_AGENT_PROVIDER_ORDER.map((provider, index) => [provider, index])
)
const codingAgentProviderRank = (provider: string) =>
  CODING_AGENT_PROVIDER_RANK.get(provider) ?? 999

export default function LLMConfigurationModal({ isOpen, onClose }: LLMConfigurationModalProps) {
  // Get current mode from app store
  const agentMode = useAppStore(state => state.agentMode)
  // Map 'simple' to 'multi-agent' for our mode-specific configs
  const currentMode: 'multi-agent' | 'workflow' = agentMode === 'workflow' ? 'workflow' : 'multi-agent'

  const {
    // Legacy configs (kept for backward compatibility)
    setAgentConfig,
    // Mode-specific configs
    getConfigForMode,
    setChatAgentConfig,
    setWorkflowAgentConfig,
    defaultsLoaded,
    loadDefaultsFromBackend,
    getProviderDynamicModels,
    // Supported providers filter
    isProviderSupported,
    llmConfigLocked,
    lockedProviders,
    providerManifest,
    providerManifestLoaded,
    loadProviderManifest,
    savedLLMs: publishedLLMs,
  } = useLLMStore()

  // Same read-only gate the workflow panel applies to these sections, so a
  // read-tier user can't add keys or publish models through the modal either.
  const readOnly = !useCanWriteWorkflow()

  const isProviderLocked = (provider: string) =>
    lockedProviders.includes('all') || lockedProviders.includes(provider)

  // Only coding-agent CLIs are configurable; direct API providers are retired.
  const manifestProviderEntries = useMemo(() => (
    installedCodingProviders(providerManifest).filter(entry =>
      isProviderSupported(entry.id as LLMProvider)
    )
  ), [isProviderSupported, providerManifest])

  const codingAgentProviderEntries = useMemo(
    () => [...manifestProviderEntries]
      .sort((a, b) =>
        codingAgentProviderRank(a.id) - codingAgentProviderRank(b.id) ||
        a.display_name.localeCompare(b.display_name)
      ),
    [manifestProviderEntries]
  )

  // Pi CLI is one manifest entry but routes through several model backends;
  // fetch its group list once (backend already returns a deduped, ordered
  // "groups" array) so the sidebar can show one tab per backend instead of a
  // single opaque "Pi CLI" option.
  const [piCliGroups, setPiCliGroups] = useState<string[]>([])
  useEffect(() => {
    if (!isOpen || !codingAgentProviderEntries.some(entry => entry.id === 'pi-cli')) return
    let cancelled = false
    getProviderDynamicModels('pi-cli', false).then(result => {
      if (cancelled || !result?.groups?.length) return
      setPiCliGroups(result.groups)
    })
    return () => { cancelled = true }
  }, [isOpen, codingAgentProviderEntries, getProviderDynamicModels])

  const codingAgentSidebarItems = useMemo(() => {
    return codingAgentProviderEntries.flatMap(entry => {
      if (entry.id !== 'pi-cli' || piCliGroups.length === 0) {
        return [{ tabId: entry.id as TabType, displayName: entry.display_name, authDescription: entry.auth_description, entry, groupFilter: undefined as string | undefined }]
      }
      // Every group shares the pi-cli entry's auth_description ("Local CLI
      // (Pi provider API key)") -- generic and correct for the underlying
      // mechanism, but names "Pi" for a sidebar item that no longer does.
      return piCliGroups.map(group => ({
        tabId: piCliGroupTabId(group),
        displayName: group,
        authDescription: 'API key',
        entry,
        groupFilter: group,
      }))
    })
  }, [codingAgentProviderEntries, piCliGroups])

  // Get mode-specific configs
  const modeConfig = getConfigForMode(currentMode)
  const modePrimaryConfig = modeConfig.primaryConfig
  const modeAgentConfig = modeConfig.agentConfig

  const setModeAgentConfig = useCallback((config: AgentLLMConfiguration | null) => {
    if (currentMode === 'workflow') {
      setWorkflowAgentConfig(config)
    } else {
      setChatAgentConfig(config)
    }
    // Also update legacy config for backward compatibility
    setAgentConfig(config)
  }, [currentMode, setChatAgentConfig, setWorkflowAgentConfig, setAgentConfig])

  // Initialize/Migrate agentConfig for current mode
  useEffect(() => {
    if (isOpen && !modeAgentConfig && modePrimaryConfig.provider && modePrimaryConfig.model_id) {
      const newConfig: AgentLLMConfiguration = {
        primary: {
          provider: modePrimaryConfig.provider,
          model_id: modePrimaryConfig.model_id,
        },
      }

      setModeAgentConfig(newConfig)
    }
  }, [isOpen, modeAgentConfig, modePrimaryConfig, setModeAgentConfig])

  const [activeTab, setActiveTab] = useState<TabType>('library')

  const openProvider = useCallback((provider: ProviderManifestEntry) => {
    if (provider.id === 'pi-cli') {
      // 'pi-cli' alone is no longer a real sidebar tab -- it's split into one
      // tab per model backend. Land on the first (Gemini, when available).
      const firstPiTab = codingAgentSidebarItems.find(item => item.entry.id === 'pi-cli')
      setActiveTab(firstPiTab ? firstPiTab.tabId : piCliGroupTabId('Gemini'))
      return
    }
    setActiveTab(provider.id as TabType)
  }, [codingAgentSidebarItems])

  // Load defaults and manifest when modal opens
  useEffect(() => {
    if (isOpen && !defaultsLoaded) {
      loadDefaultsFromBackend()
    }
    if (isOpen && !providerManifestLoaded) {
      loadProviderManifest()
    }
  }, [isOpen, defaultsLoaded, loadDefaultsFromBackend, providerManifestLoaded, loadProviderManifest])

  const getManifestEntry = useCallback((providerId: string): ProviderManifestEntry | undefined => {
    return manifestProviderEntries.find(p => p.id === providerId)
  }, [manifestProviderEntries])

  // Handle Escape key
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && isOpen) {
        onClose()
      }
    }
    if (isOpen) document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, onClose])

  if (!isOpen) return null

  if (llmConfigLocked) {
    return (
      <TooltipProvider>
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-2 sm:p-4">
          <div className="bg-background border border-border rounded-lg shadow-xl w-full max-w-md flex flex-col p-6">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-lg font-semibold text-foreground">LLM Configuration</h2>
              <Button variant="ghost" size="sm" onClick={onClose} className="h-8 w-8 p-0 hover:bg-secondary">
                <X className="w-4 h-4" />
              </Button>
            </div>
            {(() => {
              const effective = effectiveLLMUnderLock(modePrimaryConfig, true, publishedLLMs)
              const name = effective ? (providerManifest.find(p => p.id === effective.provider)?.display_name ?? effective.provider) : null
              const others = manifestProviderEntries
                .filter(p => p.id !== effective?.provider)
                .map(p => p.display_name)
                .sort((a, b) => a.localeCompare(b))
              return (
                <>
                  <p className="text-foreground">
                    {name ? <>This deployment runs on <span className="font-medium">{name}</span>{effective?.model_id && effective.model_id !== effective.provider ? <> <span className="font-mono text-sm text-muted-foreground">{effective.model_id}</span></> : null}.</> : 'This deployment runs on the model published by your administrator.'}
                  </p>
                  <p className="mt-2 text-sm text-muted-foreground">
                    Set by your administrator for every workflow and chat here; it cannot be changed from this screen.
                  </p>
                  {others.length > 0 && (
                    <div className="mt-3 text-xs text-muted-foreground">
                      <div className="mb-1 font-medium text-foreground/80">Other providers on this platform</div>
                      <div className="flex flex-wrap gap-1.5">
                        {others.map(o => (
                          <span key={o} className="inline-flex items-center gap-1 rounded-md border border-dashed border-border px-2 py-0.5 opacity-60" title="Locked on this deployment">
                            <Lock className="w-3 h-3" /> {o}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              )
            })()}
          </div>
        </div>
      </TooltipProvider>
    )
  }

  return (
    <TooltipProvider>
      <ModalPortal>
      <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-2 sm:p-4">
        <div className="bg-background border border-border rounded-lg shadow-xl w-full max-w-4xl max-h-[calc(100vh-2rem)] flex flex-col">
          {/* Header */}
          <div className="flex items-center justify-between p-6 border-b border-border flex-shrink-0">
            <div className="flex items-center gap-3">
              <Settings className="w-6 h-6 text-primary" />
              <h2 className="text-xl font-semibold text-foreground">LLM Configuration</h2>
              <span className={`text-xs px-2 py-0.5 rounded-full ${
                currentMode === 'workflow'
                  ? 'bg-purple-500/20 text-purple-400'
                  : 'bg-blue-500/20 text-blue-400'
              }`}>
                {currentMode === 'workflow' ? 'Automation' : 'Chat'}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="ghost" size="sm" onClick={onClose} className="h-8 w-8 p-0 hover:bg-secondary">
                <X className="w-4 h-4" />
              </Button>
            </div>
          </div>

          {/* Content */}
          <div className="flex flex-1 min-h-0">
            {/* Left Sidebar */}
            <div className="w-48 sm:w-64 border-r border-border bg-muted/30 p-3 sm:p-4 flex-shrink-0 overflow-y-auto">
              <div className="space-y-2">
                <h3 className="text-sm font-medium text-muted-foreground mb-3">General</h3>

                <button
                  onClick={() => setActiveTab('library')}
                  className={`w-full flex items-center gap-3 p-3 rounded-md text-left transition-colors ${
                    activeTab === 'library' ? 'bg-primary text-primary-foreground' : 'hover:bg-secondary'
                  }`}
                >
                  <div className="flex-1">
                    <div className="font-medium">Library</div>
                    <div className="text-xs opacity-75">Providers + saved</div>
                  </div>
                  <Settings className="w-4 h-4" />
                </button>

                {codingAgentSidebarItems.length > 0 && (
                  <>
                    <h3 className="text-sm font-medium text-muted-foreground mb-3 mt-6">Coding Agents</h3>
                    {codingAgentSidebarItems.map((item) => (
                      <button
                        key={item.tabId}
                        onClick={() => setActiveTab(item.tabId)}
                        className={`w-full flex items-center gap-3 p-3 rounded-md text-left transition-colors ${
                          activeTab === item.tabId ? 'bg-primary text-primary-foreground' : 'hover:bg-secondary'
                        }`}
                      >
                        <div className="flex-1">
                          <div className="font-medium">{item.displayName}</div>
                          <div className="text-xs opacity-75">
                            {isProviderLocked(item.entry.id) ? 'Configured by admin' : item.authDescription}
                          </div>
                        </div>
                        {isProviderLocked(item.entry.id) && <Lock className="w-4 h-4 opacity-60" />}
                      </button>
                    ))}
                  </>
                )}

              </div>
            </div>

            {/* Right Content */}
            <div className="flex-1 p-3 sm:p-6 overflow-y-auto min-h-0">
              {activeTab === 'library' && (
                <LibraryTab
                  providers={manifestProviderEntries}
                  onSelectProvider={openProvider}
                  isProviderLocked={isProviderLocked}
                />
              )}

              {/* Coding agent sections — unified component driven by manifest */}
              {(() => {
                const sidebarItem = codingAgentSidebarItems.find(item => item.tabId === activeTab)
                if (!sidebarItem) return null
                const entry = getManifestEntry(sidebarItem.entry.id)
                if (!entry) return <div className="text-sm text-muted-foreground py-8 text-center">Loading provider info...</div>
                return <CodingAgentSection key={sidebarItem.tabId} provider={entry} groupFilter={sidebarItem.groupFilter} readOnly={readOnly} />
              })()}
            </div>
          </div>

          {/* Footer */}
          <div className="flex items-center justify-between p-3 sm:p-6 border-t border-border bg-muted/30 flex-shrink-0">
            <div className="flex items-center gap-3">
              <div className="text-sm text-muted-foreground">
                Changes are saved automatically.
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="outline" onClick={onClose}>Close</Button>
            </div>
          </div>
        </div>
      </div>
      </ModalPortal>
    </TooltipProvider>
  )
}
