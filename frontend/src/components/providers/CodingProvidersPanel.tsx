import ProviderAccounts from './ProviderAccounts'
import CostsOverview from './CostsOverview'
import ConversationsOverview from './ConversationsOverview'
import { useCanReviewCode } from '../../hooks/useCanReviewCode'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ArrowLeft,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  HelpCircle,
  DollarSign,
  MessageSquare,
  Loader2,
  RefreshCw,
  Terminal,
  UserPlus,
  X,
} from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import {
  llmConfigService,
  type ProviderManifestEntry,
} from '../../services/llm-config-api'
import { CODING_PROVIDER_GUIDES, DEFAULT_CODING_PROVIDER_GUIDE } from './codingProviderGuides'
import GuidedProviderTerminal from './GuidedProviderTerminal'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { contextualGuideKey, rememberGuide } from '../../utils/onboarding'
import { FirstVisitTip } from '../workflow/FirstVisitTip'
import WorkflowWalkthrough from '../workflow/WorkflowWalkthrough'
import { useAuthStore } from '../../stores/useAuthStore'
import type { ProviderSetupAction, ProviderSetupSession } from '../../services/llm-config-api'

interface CodingProvidersPanelProps {
  embedded?: boolean
  isOpen: boolean
  onClose: () => void
}

const PROVIDER_SIDEBAR_NAMES: Record<string, string> = {
  'codex-cli': 'Codex',
  'cursor-cli': 'Cursor',
  'pi-cli': 'Pi',
}

const PROVIDER_SIDEBAR_ICONS: Record<string, string> = {
  'claude-code': '/provider-icons/claude.ico',
  'muse-cli': '/provider-icons/muse.ico',
  'codex-cli': '/provider-icons/codex.png',
  'cursor-cli': '/provider-icons/cursor.svg',
  'pi-cli': '/provider-icons/pi.svg',
}

type ProviderStatus = 'ready' | 'auth' | 'missing' | 'deprecated'

const providerStatus = (provider: ProviderManifestEntry): ProviderStatus => {
  if (provider.deprecated) return 'deprecated'
  if (provider.usable) return 'ready'
  if (provider.runtime_available === false) return 'missing'
  return 'auth'
}

const STATUS_STYLES: Record<ProviderStatus, { label: string; className: string }> = {
  ready: {
    label: 'Connected',
    className: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/30',
  },
  auth: {
    label: 'Needs authentication',
    className: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
  },
  missing: {
    label: 'Not installed',
    className: 'bg-gray-100 text-gray-600 ring-gray-200 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700',
  },
  deprecated: {
    label: 'Deprecated',
    className: 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-500/30',
  },
}

const GUIDED_SETUP_PROVIDERS = new Set(['claude-code', 'codex-cli', 'cursor-cli', 'pi-cli', 'muse-cli', 'agy-cli'])


function CliVersionStatus({ provider }: { provider: ProviderManifestEntry }) {
  if (!provider.installed_version && provider.update_status !== 'unsupported') return null
  return (
    <>
      {provider.installed_version && (
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-300">CLI version {provider.installed_version}</p>
      )}
      {provider.update_status === 'unsupported' && (
        <p className="mt-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
          This CLI is below the minimum supported version{provider.min_supported_version ? ` ${provider.min_supported_version}` : ''}. Update the CLI on the backend server and re-run P0 certification.
        </p>
      )}
    </>
  )
}

function StatusBadge({ provider }: { provider: ProviderManifestEntry }) {
  const status = STATUS_STYLES[providerStatus(provider)]
  return (
    <span className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset ${status.className}`}>
      {status.label}
    </span>
  )
}

function ProviderListStatus({ provider }: { provider: ProviderManifestEntry }) {
  const status = providerStatus(provider)
  const label = STATUS_STYLES[status].label
  const color = status === 'ready'
    ? 'text-emerald-500 dark:text-emerald-400'
    : status === 'auth'
      ? 'text-amber-500 dark:text-amber-400'
      : status === 'deprecated'
        ? 'text-red-500 dark:text-red-400'
        : 'text-gray-400 dark:text-gray-500'
  return (
    <span
      className={`inline-flex shrink-0 items-center ${color}`}
      title={label}
      aria-label={status === 'ready' ? `${provider.display_name} is connected` : `${provider.display_name}: ${label}`}
    >
      {status === 'ready' ? <CheckCircle2 className="h-4 w-4" /> : <CircleAlert className="h-4 w-4" />}
    </span>
  )
}

export default function CodingProvidersPanel({ isOpen, onClose, embedded = false }: CodingProvidersPanelProps) {
  const [providers, setProviders] = useState<ProviderManifestEntry[]>([])
  const [providerOrder, setProviderOrder] = useState<string[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [view, setView] = useState<'provider' | 'costs' | 'chats'>('provider')
  const [showWalkthrough, setShowWalkthrough] = useState(false)
  const [walkthroughOpenToken, setWalkthroughOpenToken] = useState(0)
  const openWalkthrough = useCallback(() => {
    rememberGuide(contextualGuideKey('providers', 'Accounts'))
    rememberGuide(contextualGuideKey('providers', 'Costs'))
    setWalkthroughOpenToken(token => token + 1)
    setShowWalkthrough(true)
  }, [])
  useEffect(() => {
    if (!isOpen) {
      setShowWalkthrough(false)
      return
    }
    window.addEventListener('open-providers-walkthrough', openWalkthrough)
    return () => window.removeEventListener('open-providers-walkthrough', openWalkthrough)
  }, [isOpen, openWalkthrough])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [guidedSession, setGuidedSession] = useState<ProviderSetupSession | null>(null)
  const [guidedStarting, setGuidedStarting] = useState<ProviderSetupAction | null>(null)
  const [guidedError, setGuidedError] = useState<string | null>(null)
  const [guidedConflictAction, setGuidedConflictAction] = useState<ProviderSetupAction | null>(null)
  const isMultiUserMode = useAuthStore(state => state.isMultiUserMode)
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const canReview = useCanReviewCode()
  useEffect(() => { if (!canReview) setView('provider') }, [canReview])
  const canRunGuidedSetup = !isMultiUserMode || isAdmin

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const manifest = await llmConfigService.getProviderManifest()
      const order = new Map(manifest.provider_order.map((id, index) => [id, index]))
      const codingAgents = manifest.providers
        .filter(provider => provider.integration_kind === 'coding_agent')
        .sort((a, b) => (order.get(a.id) ?? 999) - (order.get(b.id) ?? 999))
      setProviders(codingAgents)
      setProviderOrder(manifest.provider_order)
      setSelectedId(current => current && codingAgents.some(provider => provider.id === current)
        ? current
        : codingAgents[0]?.id ?? null)
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : 'Could not load provider status')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!isOpen) return
    void refresh()
  }, [isOpen, refresh])

  useEffect(() => {
    if (!isOpen || embedded) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !showWalkthrough) {
        if (guidedSession?.status === 'running') {
          void llmConfigService.cancelProviderSetup(guidedSession.id).catch(() => undefined)
        }
        setGuidedSession(null)
        onClose()
      }
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', closeOnEscape)
    }
  }, [guidedSession, isOpen, onClose, embedded, showWalkthrough])

  const orderedProviders = useMemo(() => {
    const order = new Map(providerOrder.map((id, index) => [id, index]))
    return [...providers].sort((a, b) => (order.get(a.id) ?? 999) - (order.get(b.id) ?? 999))
  }, [providerOrder, providers])

  const selectedProvider = orderedProviders.find(provider => provider.id === selectedId) ?? orderedProviders[0]
  const guide = selectedProvider ? (CODING_PROVIDER_GUIDES[selectedProvider.id] ?? DEFAULT_CODING_PROVIDER_GUIDE) : undefined

  // The shared server login is everyone's: confirm before signing it in, and
  // point to Add my account (Provider accounts above) for a private one.
  const [confirmSharedSetup, setConfirmSharedSetup] = useState(false)
  const signInShared = (provider: ProviderManifestEntry) => {
    if (provider.id === 'pi-cli') { void startGuidedSetup('authenticate'); return }
    setConfirmSharedSetup(true)
  }
  const startGuidedSetup = async (action: ProviderSetupAction, replaceRunning = false) => {
    if (!selectedProvider || !GUIDED_SETUP_PROVIDERS.has(selectedProvider.id)) return
    setGuidedStarting(action)
    setGuidedError(null)
    setGuidedConflictAction(null)
    try {
      const session = await llmConfigService.startProviderSetup(selectedProvider.id, action, 100, 24, undefined, replaceRunning)
      setGuidedSession(session)
    } catch (setupError) {
      const status = (setupError as { response?: { status?: number } })?.response?.status
      const responseMessage = (setupError as { response?: { data?: { error?: string } } })?.response?.data?.error
      setGuidedError(responseMessage || (setupError instanceof Error ? setupError.message : 'Could not start guided setup'))
      if (status === 409) setGuidedConflictAction(action)
    } finally {
      setGuidedStarting(null)
    }
  }

  const closePanel = () => {
    if (embedded) {
      onClose()
      return
    }
    if (guidedSession?.status === 'running') {
      void llmConfigService.cancelProviderSetup(guidedSession.id).catch(() => undefined)
    }
    setGuidedSession(null)
    onClose()
  }

  if (!isOpen && !embedded) return null

  const content = (
      <div
        className={embedded ? 'h-full min-h-0' : 'fixed inset-0 z-[1000] flex items-center justify-center bg-gray-950/55 p-2 backdrop-blur-sm sm:p-5'}
        onMouseDown={event => {
          if (!embedded && event.target === event.currentTarget) closePanel()
        }}
      >
        <WorkflowWalkthrough isOpen={isOpen && showWalkthrough} surface="providers" openToken={walkthroughOpenToken} onClose={() => setShowWalkthrough(false)} />
        <div
          role={embedded ? 'region' : 'dialog'}
          aria-modal={embedded ? undefined : true}
          aria-label="Providers"
          className={embedded
            ? 'flex h-full min-h-0 w-full flex-col overflow-hidden bg-white dark:bg-gray-900'
            : 'flex h-[min(860px,calc(100vh-1rem))] w-full max-w-6xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-gray-700 dark:bg-gray-900 sm:h-[min(860px,calc(100vh-2.5rem))]'}
        >
          {error && (
            <div className="mx-4 mt-4 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300 sm:mx-6">
              <CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />
              <span className="flex-1">{error}</span>
              <button type="button" onClick={refresh} className="font-medium underline underline-offset-2">Retry</button>
            </div>
          )}

          <div className="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-[14rem_minmax(0,1fr)]">
            <aside className="border-b border-gray-200 bg-gray-50/80 p-2 dark:border-gray-700 dark:bg-gray-950/30 md:overflow-y-auto md:border-b-0 md:border-r md:p-3">
              <div className="mb-1 flex min-h-8 items-center justify-between gap-1 pl-2">
                <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Available providers</span>
                <div className="flex shrink-0 items-center gap-0.5">
                  <FirstVisitTip
                    topic={view === 'costs' ? 'Costs' : 'Accounts'}
                    title={view === 'costs' ? 'Costs across your work' : 'Choose the account your work runs on'}
                    body={view === 'costs'
                      ? 'Review recorded spend by provider, account and workspace. Provider subscriptions and remaining plan allowance are separate.'
                      : 'Choose a provider, then use Add my account for your own login or key. Select that provider and account through Runs on in your workspace.'}
                    enabled={isOpen && !loading && !error && providers.length > 0 && !showWalkthrough && !guidedSession && !guidedStarting}
                    onLearnMore={openWalkthrough}
                  >
                  <button type="button" onClick={openWalkthrough} aria-label="Providers help & walkthrough" title="Help & walkthrough" className="rounded-lg p-1.5 text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800">
                    <HelpCircle className="h-4 w-4" />
                  </button>
                  </FirstVisitTip>
                  {view === 'provider' && <button
                    type="button"
                    onClick={refresh}
                    disabled={loading}
                    aria-label="Refresh provider status"
                    className="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100"
                  >
                    <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
                  </button>}
                  <button
                    type="button"
                    onClick={closePanel}
                    aria-label={embedded ? 'Back from providers' : 'Close providers'}
                    className="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100"
                  >
                    {embedded ? <ArrowLeft className="h-4 w-4" /> : <X className="h-4 w-4" />}
                  </button>
                </div>
              </div>
              {loading && providers.length === 0 ? (
                <div className="flex items-center gap-2 px-2 py-4 text-sm text-gray-500">
                  <Loader2 className="h-4 w-4 animate-spin" /> Checking the server…
                </div>
              ) : (
                <div data-tour="providers-list" className="flex gap-1.5 overflow-x-auto md:flex-col md:overflow-x-visible">
                  {orderedProviders.map(provider => (
                    <button
                      type="button"
                      key={provider.id}
                      onClick={() => { setSelectedId(provider.id); setView('provider') }}
                      className={`min-w-[12rem] rounded-lg border px-2.5 py-2 text-left transition-colors md:min-w-0 ${
                        view === 'provider' && selectedProvider?.id === provider.id
                          ? 'border-violet-300 bg-white shadow-sm dark:border-violet-500/50 dark:bg-gray-800'
                          : 'border-transparent hover:border-gray-200 hover:bg-white dark:hover:border-gray-700 dark:hover:bg-gray-800/70'
                      }`}
                    >
                      <div className="flex min-h-6 items-center gap-2">
                        {PROVIDER_SIDEBAR_ICONS[provider.id] ? (
                          <img src={PROVIDER_SIDEBAR_ICONS[provider.id]} alt="" className={`h-5 w-5 shrink-0 rounded object-contain ${provider.id === 'pi-cli' ? 'bg-[#f6f6f6] p-0.5' : ''}`} />
                        ) : <Terminal aria-hidden="true" className="h-5 w-5 shrink-0 text-gray-500 dark:text-gray-400" />}
                        <span className="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-gray-100">{PROVIDER_SIDEBAR_NAMES[provider.id] || provider.display_name}</span>
                        <ProviderListStatus provider={provider} />
                        <ChevronRight className={`h-3.5 w-3.5 shrink-0 text-gray-400 ${view === 'provider' && selectedProvider?.id === provider.id ? 'text-violet-500' : ''}`} />
                      </div>
                    </button>
                  ))}
                </div>
              )}
              {canReview && <>
                <div className="mb-1 mt-4 hidden px-2 text-[10px] font-semibold uppercase tracking-wider text-gray-400 md:block">Review</div>
                {([{ id: 'costs', label: 'Costs', icon: DollarSign }, { id: 'chats', label: 'Conversations', icon: MessageSquare }] as const).map(item => (
                  <button type="button" key={item.id} data-tour={`providers-${item.id}`} onClick={() => setView(item.id)} aria-pressed={view === item.id}
                    className={`mt-1.5 w-full rounded-lg border px-2.5 py-2 text-left transition-colors ${view === item.id ? 'border-violet-300 bg-white shadow-sm dark:border-violet-500/50 dark:bg-gray-800' : 'border-transparent hover:border-gray-200 hover:bg-white dark:hover:border-gray-700 dark:hover:bg-gray-800/70'}`}>
                    <div className="flex min-h-6 items-center gap-2">
                      <item.icon aria-hidden="true" className="h-5 w-5 shrink-0 text-gray-500 dark:text-gray-400" />
                      <span className="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-gray-100">{item.label}</span>
                      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-gray-400" />
                    </div>
                  </button>
                ))}
              </>}
              {/* Product defaults screen removed (2026-09-30): the Runs on choice when creating a
                  workflow, Crew or Code replaces it. AGENTWORKS_PRODUCT_DEFAULTS still applies. */}
            </aside>

            <main className="min-h-0 overflow-y-auto px-4 py-5 sm:px-7 sm:py-6">
              {canReview && view === 'costs' && <CostsOverview />}
              {canReview && view === 'chats' && <ConversationsOverview />}
              {selectedProvider && <ConfirmationDialog
                isOpen={confirmSharedSetup}
                onClose={() => setConfirmSharedSetup(false)}
                onConfirm={() => { setConfirmSharedSetup(false); void startGuidedSetup('authenticate') }}
                title={`Sign in the shared ${selectedProvider.display_name} login?`}
                message="This is the shared account, not yours. Everyone it is available to will run on the login you sign in with, and on its plan. To add a login only you use, cancel and choose \u201cAdd my account\u201d under Provider accounts."
                confirmText="Sign in shared login"
                type="warning"
              />}

              {view === 'provider' && !loading && orderedProviders.length === 0 && !error && (
                <div className="flex h-full items-center justify-center text-sm text-gray-500">No coding providers are available.</div>
              )}

              {view === 'provider' && selectedProvider && guide && (
                <div className="mx-auto max-w-6xl">
                  {isMultiUserMode && !isAdmin && (
                    <section role="note" className="mb-6 rounded-xl border border-violet-200 bg-violet-50/70 p-4 dark:border-violet-500/30 dark:bg-violet-500/10">
                      <div className="flex items-start gap-3">
                        <UserPlus className="mt-0.5 h-4 w-4 shrink-0 text-violet-600 dark:text-violet-300" />
                        <div>
                          <p className="text-sm font-medium text-violet-950 dark:text-violet-100">Set up your own account</p>
                          <p className="mt-1 text-sm leading-6 text-violet-800/80 dark:text-violet-200/80">
                            An administrator manages the shared account, and it may not be available to you. Choose <strong>Add my account</strong> under Provider accounts below to sign in with your own login or key. Only you can use it unless you share it.
                          </p>
                        </div>
                      </div>
                    </section>
                  )}
                  <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <h2 className="text-xl font-semibold text-gray-950 dark:text-white">{selectedProvider.display_name}</h2>
                        <StatusBadge provider={selectedProvider} />
                      </div>
                      <p className="mt-1 max-w-2xl text-sm leading-6 text-gray-600 dark:text-gray-300">{selectedProvider.description}</p>
                      <CliVersionStatus provider={selectedProvider} />
                    </div>
                  </div>

                  <div data-tour="provider-accounts">
                    <ProviderAccounts key={selectedProvider.id} provider={selectedProvider.id} providerLabel={PROVIDER_SIDEBAR_NAMES[selectedProvider.id] || selectedProvider.display_name} />
                  </div>

                  {selectedProvider.deprecated && selectedProvider.deprecation_reason && (
                    <div className="mb-5 rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
                      {selectedProvider.deprecation_reason}
                    </div>
                  )}

                  {guidedSession && guidedSession.provider === selectedProvider.id && (
                    <GuidedProviderTerminal
                      session={guidedSession}
                      onFinished={finishedSession => {
                        setGuidedSession(finishedSession)
                        void refresh()
                      }}
                      onClose={() => setGuidedSession(null)}
                    />
                  )}

                  {guidedError && (
                    <div className="mb-5 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
                      <CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />
                      <div className="flex-1">
                        <p>{guidedError}</p>
                        {guidedConflictAction && (
                          <button
                            type="button"
                            onClick={() => {
                              if (window.confirm(`End the existing ${selectedProvider.display_name} setup session and start a new one?`)) {
                                void startGuidedSetup(guidedConflictAction, true)
                              }
                            }}
                            disabled={guidedStarting !== null}
                            className="mt-2 rounded-lg border border-red-300 bg-white px-3 py-1.5 text-xs font-semibold text-red-700 hover:bg-red-100 disabled:opacity-50 dark:border-red-500/50 dark:bg-gray-900 dark:text-red-300 dark:hover:bg-red-500/10"
                          >
                            End existing session and start new
                          </button>
                        )}
                      </div>
                    </div>
                  )}

                  {/* Signing in and usage live on the account rows above (and their terminal);
                      the page only adds what the accounts cannot show. */}
                  {selectedProvider.runtime_available !== true && (
                    <div className="mb-5 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-200">
                      <p>This CLI is not installed on this server. A platform administrator must repair or update the deployment.</p>
                      {canRunGuidedSetup && selectedProvider.install_command && (
                        <pre className="mt-2 overflow-x-auto rounded-md bg-gray-950 px-3 py-2 text-xs text-gray-100"><code>{selectedProvider.install_command}</code></pre>
                      )}
                    </div>
                  )}
                  {selectedProvider.id === 'pi-cli' && selectedProvider.runtime_available === true && canRunGuidedSetup && (
                    <section className="mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700">
                      <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Model providers</h3>
                      <p className="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{guide.authenticateNote}</p>
                      <button
                        type="button"
                        onClick={() => signInShared(selectedProvider)}
                        disabled={guidedStarting !== null || guidedSession?.status === 'running'}
                        className="mt-3 inline-flex items-center gap-2 rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
                      >
                        {guidedStarting === 'authenticate' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Terminal className="h-4 w-4" />}
                        {selectedProvider.auth_configured ? 'Manage connections' : 'Connect a provider'}
                      </button>
                    </section>
                  )}
                  {!selectedProvider.auth_configured && !GUIDED_SETUP_PROVIDERS.has(selectedProvider.id) && selectedProvider.runtime_available === true && (
                    <p className="mb-5 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
                      Guided sign-in for this provider is not available yet. A platform administrator must configure its credentials.
                    </p>
                  )}

                </div>
              )}
            </main>
          </div>
        </div>
      </div>
  )
  return embedded ? content : <ModalPortal>{content}</ModalPortal>
}
