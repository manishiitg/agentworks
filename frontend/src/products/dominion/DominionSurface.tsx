import { useWorkspaceViewPreference } from '../../hooks/useWorkspaceViewPreference'
import { normalizeViewFrom } from '../../utils/workspaceViewPreference'
import { useEffect, useState } from 'react'
import { MessageCircle, Plus, TrendingUp } from 'lucide-react'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import ChatArea from '../../components/ChatArea'
import { ProductSurfaceSwitcher } from '../../components/ProductSurfaceSwitcher'
import AccountControl from '../../components/topbar/AccountControl'
import { agentApi } from '../../services/api'
import { useAppStore } from '../../stores/useAppStore'
import { useChatStore, waitForChatStoreHydration } from '../../stores/useChatStore'
import { useModeStore } from '../../stores/useModeStore'
import { restoreSession } from '../../utils/sessionRestore'
import { AddStockDialog } from './AddStockDialog'
import { ConfirmDialog } from './ConfirmDialog'
import { DailyActions } from './DailyActions'
import { loadEquityCurve, loadLatestSnapshot } from './adapters/portfolio'
import { loadOpenPositions } from './adapters/positions'
import { loadRecentTradeIdeas } from './adapters/signals'
import { loadRecentTradeOutcomes } from './adapters/outcomes'
import { loadWatchlist, saveWatchlist } from './adapters/watchlist'
import { computeWinRate } from './adapters/winRate'
import { StatTile } from './StatTile'
import { StockDetailView } from './StockDetailView'
import { StockTable } from './StockTable'
import { groupBySymbol } from './stockGroups'
import type { EquityPoint, OpenPosition, PortfolioSnapshot, TradeIdea, TradeOutcome, WatchlistItem, WatchlistTier } from './types'

const DOMINION_PROFILE_ID = 'dominion'

function DominionChatWelcome() {
  return (
    <div className="flex h-full items-center justify-center px-6 py-10">
      <div className="max-w-xs text-center">
        <span className="mx-auto grid h-12 w-12 place-items-center rounded-2xl bg-indigo-500/10 text-indigo-400">
          <MessageCircle className="h-5 w-5" />
        </span>
        <h2 className="mt-4 text-base font-semibold text-slate-100">Ask about your portfolio</h2>
        <p className="mt-2 text-sm leading-6 text-slate-400">
          Read-only over the paper-trading workflow's signals, positions, and trade history. It can answer and
          explain -- it won't change the watchlist or place trades.
        </p>
      </div>
    </div>
  )
}

// Finds-or-creates the one singleton Dominion chat tab, same pattern as
// Finance's own useFinanceChatTab.
function useDominionChatTab() {
  const [tabId, setTabId] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const prepare = async () => {
      useModeStore.getState().setModeCategory('multi-agent')
      useAppStore.getState().setAgentMode('multi-agent')
      await waitForChatStoreHydration()
      if (cancelled) return
      const chatStore = useChatStore.getState()
      const existing = Object.values(chatStore.chatTabs).find((tab) => tab.metadata?.agentProfileId === DOMINION_PROFILE_ID)
      const conversation = await agentApi.resolveAgentProfileConversation(
        DOMINION_PROFILE_ID,
        { conversation_key: 'main' },
        existing?.sessionId ?? undefined,
      )

      // createChatTab reuses the durable singleton and merges the current
      // profile binding, upgrading pre-contract Dominion tabs in place.
      const createdTabId = await chatStore.createChatTab('Dominion', {
        mode: 'multi-agent',
        agentProfileId: DOMINION_PROFILE_ID,
        agentProfileVersion: 1,
        agentProfileWorkspace: 'Chats',
        agentProfileProjectTitle: 'Dominion',
        agentProfileChatContract: 'profile-v1',
        agentProfileConversationKey: 'main',
        agentProfileConversationId: conversation.conversation_id,
      }, conversation.session_id)
      const tab = chatStore.getTab(createdTabId)
      if (cancelled || !tab) return

      const restoredTabId = await restoreSession(conversation.session_id, {
        title: 'Dominion',
        source: 'dominion-open',
        skipConfigRestore: true,
      })
      if (cancelled) return
      chatStore.switchTab(restoredTabId)
      setTabId(restoredTabId)
    }
    void prepare().catch((error) => {
      if (!cancelled) useChatStore.getState().addToast(error instanceof Error ? error.message : 'Unable to open Dominion conversation', 'error')
    })
    return () => { cancelled = true }
  }, [])

  return tabId
}

type LoadState = {
  snapshot: PortfolioSnapshot | null
  equityCurve: EquityPoint[]
  positions: OpenPosition[]
  ideas: TradeIdea[]
  outcomes: TradeOutcome[]
  watchlist: WatchlistItem[]
  loading: boolean
  error: string | null
}

const EMPTY_STATE: LoadState = {
  snapshot: null,
  equityCurve: [],
  positions: [],
  ideas: [],
  outcomes: [],
  watchlist: [],
  loading: true,
  error: null,
}

function useDominionData(): LoadState {
  const [state, setState] = useState<LoadState>(EMPTY_STATE)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const [snapshot, equityCurve, positions, ideas, outcomes, watchlist] = await Promise.all([
          loadLatestSnapshot(),
          loadEquityCurve(),
          loadOpenPositions(),
          loadRecentTradeIdeas(),
          loadRecentTradeOutcomes(),
          loadWatchlist(),
        ])
        if (cancelled) return
        setState({ snapshot, equityCurve, positions, ideas, outcomes, watchlist, loading: false, error: null })
      } catch (err) {
        if (cancelled) return
        setState((prev) => ({ ...prev, loading: false, error: err instanceof Error ? err.message : String(err) }))
      }
    })()
    return () => { cancelled = true }
  }, [])

  return state
}

function formatUsd(value: number, compact = false): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: compact ? 1 : 0,
    notation: compact ? 'compact' : 'standard',
  }).format(value)
}

function formatPct(value: number): string {
  return `${value >= 0 ? '+' : ''}${value.toFixed(2)}%`
}

function formatDateTime(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

const normalizeDominionTab = normalizeViewFrom(['daily-action', 'equity', 'stocks'] as const)

export function SectionHeader({ icon: Icon, title, count }: { icon: typeof TrendingUp; title: string; count?: number }) {
  return (
    <div className="mb-4 flex items-center gap-3">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-indigo-500/10 text-indigo-400">
        <Icon className="h-4 w-4" strokeWidth={2.25} />
      </span>
      <h2 className="text-base font-semibold text-slate-100">{title}</h2>
      {count != null && (
        <span className="rounded-full bg-white/5 px-2.5 py-0.5 text-xs font-medium text-slate-400">{count}</span>
      )}
    </div>
  )
}

export const CARD = 'rounded-2xl border border-white/10 bg-[#0d111c] p-6 shadow-xl shadow-black/20'

// Equity moves in a tight band (e.g. $97.4K-$100K) relative to a $0-anchored
// axis -- an unpadded/zero-anchored domain crushes every real move into a
// sliver near the top. Zoom the y-axis to the data's own range instead.
function equityDomain([dataMin, dataMax]: readonly [number, number]): [number, number] {
  const padding = Math.max((dataMax - dataMin) * 0.15, 200)
  return [Math.floor(dataMin - padding), Math.ceil(dataMax + padding)]
}

export function DominionSurface() {
  const { snapshot, equityCurve, positions, ideas, outcomes, watchlist, loading, error } = useDominionData()
  const chatTabId = useDominionChatTab()
  // Set only after a successful add/remove, so the Stocks table reflects an
  // edit immediately instead of only after a reload. Before that,
  // effectiveWatchlist just mirrors the hook's loaded value.
  const [watchlistOverride, setWatchlistOverride] = useState<WatchlistItem[] | null>(null)
  const effectiveWatchlist = watchlistOverride ?? watchlist

  const [isAddDialogOpen, setIsAddDialogOpen] = useState(false)
  const [detailSymbol, setDetailSymbol] = useState<string | null>(null)
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  // Daily Action leads: it answers "what did this workflow actually do" before
  // the reference views (equity history, full watchlist) — same insight-first
  // ordering as the platform's own report-authoring guidance.
  const [activeTab, setActiveTab] = useWorkspaceViewPreference('dominion', 'main', 'daily-action', normalizeDominionTab)

  const handleAddSymbol = async (symbol: string, tier: WatchlistTier) => {
    const next = [...effectiveWatchlist, { symbol, tier }]
    await saveWatchlist(next)
    setWatchlistOverride(next)
  }

  const handleRemoveSymbol = async (symbol: string) => {
    const next = effectiveWatchlist.filter((item) => item.symbol !== symbol)
    await saveWatchlist(next)
    setWatchlistOverride(next)
  }

  const startingEquity = equityCurve[0]?.equity ?? snapshot?.equity ?? 0
  const currentEquity = snapshot?.equity ?? 0
  const pnlDollar = currentEquity - startingEquity
  const pnlPct = startingEquity !== 0 ? (pnlDollar / startingEquity) * 100 : 0
  const todaysChange = snapshot ? snapshot.equity - snapshot.lastEquity : 0
  const winRate = computeWinRate(outcomes)
  const chartData = equityCurve.map((point) => ({ dateLabel: formatDateTime(point.snapshotAt), equity: point.equity }))
  const stockGroups = groupBySymbol(effectiveWatchlist, positions, ideas, outcomes)

  return (
    <div className="flex h-screen min-h-0 flex-col overflow-hidden bg-[#05070d]">
      <header className="flex h-[62px] shrink-0 items-center gap-4 border-b border-white/10 bg-[#05070d] px-4">
        <ProductSurfaceSwitcher />
        <div className="ml-auto flex items-center gap-1">
          <AccountControl />
        </div>
      </header>
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-[420px] shrink-0 flex-col border-r border-white/10 bg-[#070a12]">
          {chatTabId ? (
            <ChatArea tabId={chatTabId} onNewChat={() => {}} landingContent={<DominionChatWelcome />} inputVariant="product" hideRuntimeStatus showNewChatAction />
          ) : (
            <div className="grid h-full place-items-center text-xs text-slate-500">Connecting…</div>
          )}
        </aside>

        {loading ? (
          <div className="grid flex-1 place-items-center text-sm text-slate-500">Loading portfolio…</div>
        ) : error ? (
          <div className="grid flex-1 place-items-center text-sm text-red-400">Failed to load: {error}</div>
        ) : detailSymbol ? (
          <div className="flex-1 overflow-y-auto">
            <StockDetailView
              symbol={detailSymbol}
              group={stockGroups.find((g) => g.symbol === detailSymbol)}
              onBack={() => setDetailSymbol(null)}
            />
          </div>
        ) : (
          <div className="flex-1 overflow-y-auto">
            <div className="mx-auto w-full max-w-[1600px] space-y-8 p-8">

              {/* KPI row */}
              <section className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-5">
                <StatTile label="Equity" value={formatUsd(currentEquity)} />
                <StatTile label="Starting Balance" value={formatUsd(startingEquity)} />
                <StatTile
                  label="P&L"
                  value={`${pnlDollar >= 0 ? '+' : ''}${formatUsd(pnlDollar)}`}
                  detail={formatPct(pnlPct)}
                  tone={pnlDollar >= 0 ? 'positive' : 'negative'}
                />
                <StatTile
                  label="Today's Change"
                  value={`${todaysChange >= 0 ? '+' : ''}${formatUsd(todaysChange)}`}
                  tone={todaysChange >= 0 ? 'positive' : 'negative'}
                />
                <StatTile
                  label="Win Rate"
                  value={winRate.winRatePct != null ? `${winRate.winRatePct.toFixed(1)}%` : '—'}
                  detail={`${winRate.wins}W / ${winRate.losses}L / ${winRate.flat}F · Σ${winRate.sumR.toFixed(2)}R`}
                />
              </section>

              {/* Tabs -- each view is a distinct question (what happened, equity
                  history, full watchlist); showing one at a time instead of
                  stacking every section keeps the page from turning into an
                  ever-scrolling wall. */}
              <div className="border-b border-white/10">
                <nav className="-mb-px flex gap-6" role="tablist">
                  {(
                    [
                      { id: 'daily-action', label: 'Daily Action' },
                      { id: 'equity', label: 'Equity' },
                      { id: 'stocks', label: 'Stocks', count: stockGroups.length },
                    ] as const
                  ).map((tab) => (
                    <button
                      key={tab.id}
                      type="button"
                      role="tab"
                      aria-selected={activeTab === tab.id}
                      onClick={() => setActiveTab(tab.id)}
                      className={`flex items-center gap-2 border-b-2 px-1 pb-3 text-sm font-medium transition ${
                        activeTab === tab.id
                          ? 'border-indigo-400 text-slate-100'
                          : 'border-transparent text-slate-500 hover:text-slate-300'
                      }`}
                    >
                      {tab.label}
                      {'count' in tab ? (
                        <span className="rounded-full bg-white/5 px-2 py-0.5 text-xs font-medium text-slate-400">{tab.count}</span>
                      ) : null}
                    </button>
                  ))}
                </nav>
              </div>

              {activeTab === 'equity' && (
                <section>
                  <SectionHeader icon={TrendingUp} title="Equity Curve" />
                  <div className={CARD}>
                    <div className="h-[320px]">
                      {chartData.length > 0 ? (
                        <ResponsiveContainer width="100%" height="100%">
                          <AreaChart data={chartData} margin={{ top: 8, right: 16, left: 0, bottom: 0 }}>
                            <defs>
                              <linearGradient id="dominionEquityFill" x1="0" y1="0" x2="0" y2="1">
                                <stop offset="0%" stopColor="#818cf8" stopOpacity={0.4} />
                                <stop offset="100%" stopColor="#818cf8" stopOpacity={0} />
                              </linearGradient>
                            </defs>
                            <CartesianGrid strokeDasharray="3 3" stroke="#ffffff" opacity={0.06} />
                            <XAxis dataKey="dateLabel" fontSize={12} tick={{ fill: '#64748b' }} axisLine={{ stroke: '#ffffff1a' }} tickLine={false} minTickGap={50} />
                            <YAxis
                              domain={equityDomain}
                              fontSize={12}
                              tick={{ fill: '#64748b' }}
                              axisLine={false}
                              tickLine={false}
                              tickFormatter={(v) => formatUsd(Number(v), true)}
                              width={72}
                            />
                            <Tooltip
                              formatter={(value) => formatUsd(Number(value ?? 0))}
                              contentStyle={{ borderRadius: 8, fontSize: 13, background: '#0d111c', border: '1px solid rgba(255,255,255,0.1)', color: '#e2e8f0' }}
                            />
                            <Area type="monotone" dataKey="equity" stroke="#818cf8" strokeWidth={2.5} fill="url(#dominionEquityFill)" activeDot={{ r: 5 }} />
                          </AreaChart>
                        </ResponsiveContainer>
                      ) : (
                        <div className="grid h-full place-items-center text-sm text-slate-500">No snapshots yet</div>
                      )}
                    </div>
                  </div>
                </section>
              )}

              {/* Daily Action -- plain-language "what did this workflow actually do"
                  record: the latest trading session's closed trades and new signals. */}
              {activeTab === 'daily-action' && <DailyActions outcomes={outcomes} ideas={ideas} />}

              {/* Stocks -- everything grouped per symbol: tier, signal, position, recent grades.
                  Click a row for full history; the trash icon removes it from the watchlist. */}
              {activeTab === 'stocks' && (
                <section>
                  <div className="mb-4 flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-indigo-500/10 text-indigo-400">
                        <TrendingUp className="h-4 w-4" strokeWidth={2.25} />
                      </span>
                      <h2 className="text-base font-semibold text-slate-100">Stocks</h2>
                      <span className="rounded-full bg-white/5 px-2.5 py-0.5 text-xs font-medium text-slate-400">{stockGroups.length}</span>
                    </div>
                    <button
                      type="button"
                      onClick={() => setIsAddDialogOpen(true)}
                      className="flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-indigo-500"
                    >
                      <Plus className="h-3.5 w-3.5" />
                      Add Stock
                    </button>
                  </div>
                  <StockTable groups={stockGroups} onSelectSymbol={setDetailSymbol} onRequestRemove={setRemoveTarget} />
                </section>
              )}
            </div>
          </div>
        )}
      </div>

      {isAddDialogOpen && (
        <AddStockDialog existing={effectiveWatchlist} onAdd={handleAddSymbol} onClose={() => setIsAddDialogOpen(false)} />
      )}
      {removeTarget && (
        <ConfirmDialog
          title="Remove Stock"
          message={`Remove ${removeTarget} from the watchlist? This won't delete its trade history.`}
          confirmLabel="Remove"
          onConfirm={() => handleRemoveSymbol(removeTarget)}
          onClose={() => setRemoveTarget(null)}
        />
      )}
    </div>
  )
}
