import React from 'react'
import { inputTokens, totalTokens, tokenSummary, pricingCoverageText } from '../../../utils/costTokens'
import type { CostAggregate } from '../../../services/api-types'
import { Loader2, TrendingUp } from 'lucide-react'
import { phaseLabel as costPhaseLabel } from '../../../utils/costActivityBreakdown'
import { formatUSD, formatTokens, formatDuration, getRunFolderDisplayName } from './helpers'
import { buildModelCostRows } from './CostsModelSection'
import type { CostsData } from './useCostsData'

const recordedCost = (usage: CostAggregate) =>
  usage.total_cost_usd === 0 && (usage.unpriced_call_count ?? 0) > 0 ? 'Not priced' : formatUSD(usage.total_cost_usd)

type CostsDailySectionProps = Pick<
  CostsData,
  | 'scopedCosts'
  | 'hasScopedActivity'
  | 'activityBreakdown'
  | 'combinedDailyCostSummaries'
  | 'dailyActivityBreakdown'
  | 'runDailyCostSummaries'
  | 'expandedDailyDate'
  | 'setExpandedDailyDate'
  | 'costHistory'
  | 'loadingOlder'
  | 'loadOlderCosts'
> & { projectMode?: boolean }

const CostsDailySection: React.FC<CostsDailySectionProps> = ({
  scopedCosts,
  hasScopedActivity,
  activityBreakdown,
  combinedDailyCostSummaries,
  dailyActivityBreakdown,
  runDailyCostSummaries,
  expandedDailyDate,
  setExpandedDailyDate,
  costHistory,
  loadingOlder,
  loadOlderCosts,
  projectMode = false,
}) => (
  <>
              {/* Canonical product activity hierarchy */}
              {hasScopedActivity && (
                <section className="space-y-3">
                  <div>
                    <h3 className="text-sm font-semibold text-foreground">Cost by activity</h3>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {projectMode
                        ? 'Project chat and background-agent costs from the authoritative event ledger.'
                        : 'Builder, Pulse, workflow, and evaluation costs from the authoritative event ledger.'}
                    </p>
                  </div>
                  <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
                    {activityBreakdown.filter(category => !projectMode || category.total.total_cost_usd > 0 || totalTokens(category.total) > 0 || (category.total.unpriced_call_count ?? 0) > 0).map(category => {
                      return (
                      <div key={category.id} className="flex items-center gap-3 rounded-lg border border-border bg-card p-4 shadow-sm">
                        <div className="min-w-0 flex-1">
                          <div className="font-semibold text-foreground">{projectMode && category.id === 'builder' ? 'Chat' : category.label}</div>
                          <div className="truncate text-xs text-muted-foreground">{projectMode && category.id === 'builder' ? 'Project conversation and background coding tasks' : category.description}</div>
                        </div>
                          <div className="text-right">
                            <div className="font-mono font-semibold text-foreground">{recordedCost(category.total)}</div>
                            <div className="text-xs text-muted-foreground">{tokenSummary(category.total)}</div>
                            <div className="text-xs text-muted-foreground">{pricingCoverageText(category.total)}</div>
                            <div className="text-xs text-muted-foreground">LLM time: {formatDuration(category.total.llm_generation_duration_ms)}</div>
                          </div>
                      </div>
                      )
                    })}
                  </div>
                </section>
              )}

              {/* Daily Costs */}
              {combinedDailyCostSummaries.length > 0 && (
                <div className="bg-card border border-border rounded-lg p-4 shadow-sm">
                  <div className="flex items-start justify-between gap-4 mb-4">
                    <div>
                      <h3 className="text-sm font-semibold text-foreground mb-1 flex items-center gap-2">
                        <TrendingUp className="w-4 h-4 text-primary" />
                        Daily Cost Breakdown
                      </h3>
                      <p className="text-xs text-muted-foreground">
                        {projectMode ? 'Daily project totals by UTC accounting date.' : 'Daily totals by UTC accounting date using the same Builder, Pulse, Workflow, and Evaluation categories above.'}
                      </p>
                    </div>
                  </div>

                  <div className="overflow-x-auto">
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="text-muted-foreground border-b border-border pb-2">
                          <th className="text-left font-medium pb-2">Date (UTC)</th>
                          {!projectMode && <th className="text-right font-medium pb-2">Runs</th>}
                          <th className="text-right font-medium pb-2">{projectMode ? 'Chat' : 'Builder'}</th>
                          {!projectMode && <th className="text-right font-medium pb-2">Pulse</th>}
                          {!projectMode && <th className="text-right font-medium pb-2">Workflow</th>}
                          {!projectMode && <th className="text-right font-medium pb-2">Evaluation</th>}
                          <th className="text-right font-medium pb-2">LLM time</th>
                          <th className="text-right font-medium pb-2">Input / output</th>
                          <th className="text-right font-medium pb-2">Total</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border">
                        {combinedDailyCostSummaries.map(entry => {
                          const isExpanded = expandedDailyDate === entry.date
                          const categories = dailyActivityBreakdown.get(entry.date)
                          const dayUsage = scopedCosts?.by_date?.[entry.date]
                          const modelRows = buildModelCostRows({ by_model: scopedCosts?.by_date?.[entry.date]?.by_model || {} })
                          const dailyRuns = runDailyCostSummaries.filter(run => run.date === entry.date)
                          return (
                            <React.Fragment key={entry.date}>
                              <tr className="hover:bg-accent/50 transition-colors">
                                <td className="py-2">
                                  <div className="flex items-center gap-2">
                                    <span className="font-medium text-foreground">{entry.date}</span>
                                    <button
                                      type="button"
                                      onClick={() => setExpandedDailyDate(current => current === entry.date ? null : entry.date)}
                                      className="rounded border border-border px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground"
                                      aria-expanded={isExpanded}
                                    >
                                      <span aria-hidden="true" className="text-sm leading-none">{isExpanded ? '−' : '+'}</span>
                                      <span className="sr-only">{isExpanded ? 'Hide daily activity details' : 'Show daily activity details'}</span>
                                    </button>
                                  </div>
                                </td>
                                {!projectMode && <td className="py-2 text-right font-mono text-muted-foreground">{entry.runCount.toLocaleString()}</td>}
                                <td className="py-2 text-right font-mono text-muted-foreground">
                                  <div>{formatUSD(entry.builderCost)}</div>
                                  <div className="text-[10px] text-muted-foreground/70">{formatTokens(entry.builderTokens)} tok</div>
                                </td>
                                {!projectMode && <td className="py-2 text-right font-mono text-muted-foreground">
                                  <div>{entry.pulseCost === null ? '—' : formatUSD(entry.pulseCost)}</div>
                                  {entry.pulseTokens !== null && (
                                    <div className="text-[10px] text-muted-foreground/70">{formatTokens(entry.pulseTokens)} tok</div>
                                  )}
                                </td>}
                                {!projectMode && <td className="py-2 text-right font-mono text-muted-foreground">
                                  <div>{formatUSD(entry.workflowCost)}</div>
                                  <div className="text-[10px] text-muted-foreground/70">{formatTokens(entry.workflowTokens)} tok</div>
                                </td>}
                                {!projectMode && <td className="py-2 text-right font-mono text-muted-foreground">
                                  <div>{formatUSD(entry.evaluationCost)}</div>
                                  <div className="text-[10px] text-muted-foreground/70">{formatTokens(entry.evaluationTokens)} tok</div>
                                </td>}
                                <td className="py-2 text-right font-mono text-muted-foreground">{formatDuration(entry.llmDurationMS)}</td>
                                <td className="py-2 text-right font-mono text-muted-foreground">{entry.totalInputTokens !== undefined ? `${formatTokens(entry.totalInputTokens)} input · ${formatTokens(entry.totalOutputTokens)} output` : `${formatTokens(entry.totalTokens)} tokens`}</td>
                                <td className="py-2 text-right font-bold text-green-600 dark:text-green-400">{entry.totalCost === 0 && (dayUsage?.unpriced_call_count ?? 0) > 0 ? 'Not priced' : formatUSD(entry.totalCost)}</td>
                              </tr>
                              {isExpanded && (
                                <tr className="bg-muted/20">
                                  <td colSpan={projectMode ? 5 : 9} className="p-3">
                                    {!categories && modelRows.length === 0 && dailyRuns.length === 0 ? (
                                      <p className="text-xs text-muted-foreground">This older daily record has totals but no activity attribution.</p>
                                    ) : (
                                      <div className="space-y-3">
                                        {dailyRuns.length > 0 && (
                                          <div className="overflow-hidden rounded-md border border-border bg-card">
                                            <div className="border-b border-border px-3 py-2">
                                              <div className="text-xs font-semibold text-foreground">Workflow runs</div>
                                              <div className="text-[10px] text-muted-foreground">Repriced immutable run detail contributing to this date.</div>
                                            </div>
                                            <div className="divide-y divide-border">
                                              {dailyRuns.map(run => (
                                                <div key={`${run.scope}:${run.runFolder}`} className="px-3 py-2 text-[10px]">
                                                  <div className="flex flex-wrap items-center gap-2">
                                                    <span className="font-mono font-medium text-foreground">{getRunFolderDisplayName(run.runFolder)}</span>
                                                    <span className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground">{run.scope}</span>
                                                    <span className="ml-auto font-mono text-muted-foreground">{formatTokens(run.summary.totalInputTokens)} input · {formatTokens(run.summary.totalOutputTokens)} output</span>
                                                    <span className="font-mono font-semibold text-foreground">{formatUSD(run.summary.totalCost)}</span>
                                                  </div>
                                                  <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground">
                                                    {Object.entries(run.summary.stageCosts)
                                                      .filter(([, cost]) => cost > 0)
                                                      .map(([stage, cost]) => <span key={stage}>{stage}: <span className="font-mono">{formatUSD(cost)}</span></span>)}
                                                    {Object.entries(run.tokenUsage.by_model || {}).map(([model]) => <span key={model} className="font-mono">{model}</span>)}
                                                  </div>
                                                </div>
                                              ))}
                                            </div>
                                          </div>
                                        )}
                                        {modelRows.length > 0 && (
                                          <div className="overflow-hidden rounded-md border border-border bg-card">
                                            <div className="border-b border-border px-3 py-2">
                                              <div className="text-xs font-semibold text-foreground">Model split</div>
                                              <div className="text-[10px] text-muted-foreground">Actual coding provider and model recorded for this date.</div>
                                            </div>
                                            <div className="overflow-x-auto px-3 pb-2">
                                              <table className="w-full text-[10px]">
                                                <thead>
                                                  <tr className="border-b border-border text-muted-foreground">
                                                    <th className="py-1.5 text-left font-medium">Agent</th>
                                                    <th className="py-1.5 text-left font-medium">Model</th>
                                                    <th className="py-1.5 text-right font-medium">Input tokens</th>
                                                    <th className="py-1.5 text-right font-medium">Output tokens</th>
                                                    <th className="py-1.5 text-right font-medium">Cost</th>
                                                  </tr>
                                                </thead>
                                                <tbody className="divide-y divide-border">
                                                  {modelRows.map(row => (
                                                    <tr key={`${row.provider}:${row.modelId}`}>
                                                      <td className="py-1.5 pr-3 font-medium text-foreground">
                                                        <div>{row.agentLabel}</div>
                                                        {row.provider && <div className="font-mono text-[9px] font-normal text-muted-foreground">{row.provider}</div>}
                                                      </td>
                                                      <td className="py-1.5 pr-3 font-mono text-foreground">{row.modelId}</td>
                                                      <td className="py-1.5 text-right font-mono text-muted-foreground">{formatTokens(inputTokens(row.usage))}</td>
                                                      <td className="py-1.5 text-right font-mono text-muted-foreground">{formatTokens(row.usage.completion_tokens)}</td>
                                                      <td className="py-1.5 text-right font-mono font-medium text-foreground">{recordedCost(row.usage)}</td>
                                                    </tr>
                                                  ))}
                                                </tbody>
                                              </table>
                                            </div>
                                          </div>
                                        )}
                                        {(categories || []).filter(category => !projectMode || category.total.total_cost_usd > 0 || totalTokens(category.total) > 0 || (category.total.unpriced_call_count ?? 0) > 0).map(category => {
                                                                                    return (
                                            <div key={category.id} className="overflow-hidden rounded-md border border-border bg-card">
                                              <div className="flex items-center justify-between gap-3 border-b border-border px-3 py-2">
                                                <div className="text-xs font-semibold text-foreground">
                                                  {projectMode && category.id === 'builder' ? 'Chat' : category.id === 'pulse' ? 'Pulse (including background agents)' : category.label}
                                                </div>
                                                  <div className="text-right text-[10px] text-muted-foreground">
                                                    <div className="font-mono text-foreground">{recordedCost(category.total)}</div>
                                                    <div>{tokenSummary(category.total)}</div>
                                                    <div>LLM time: {formatDuration(category.total.llm_generation_duration_ms)}</div>
                                                </div>
                                              </div>
                                              {category.executions.length === 0 ? (
                                                <p className="px-3 py-2 text-[10px] text-muted-foreground">No activity recorded.</p>
                                              ) : (
                                                <div className="max-h-48 overflow-y-auto px-3">
                                                  {category.executions.map(execution => {
                                                    // PLAT-166/PLAT-167: an execution row's own combined total can hide a
                                                    // phase breakdown underneath — a workflow step's reflection turn
                                                    // sharing the step's execution id, or a message_sequence step's
                                                    // individual items each tagged with their own "item:<id>" phase.
                                                    // Most executions (chat, builder, evaluation, Pulse, a step with no
                                                    // reflection turn) never populate by_phase at all — the backend
                                                    // only writes an entry for a turn it explicitly tagged, not a
                                                    // catch-all "the rest" bucket (PLAT-166 scope-fix).
                                                    const phaseEntries = Object.entries(execution.cost.by_phase || {})
                                                      .filter(([, phaseCost]) => (
                                                        phaseCost.total_cost_usd > 0 ||
                                                        totalTokens(phaseCost) > 0 ||
                                                        (phaseCost.llm_generation_duration_ms || 0) > 0
                                                      ))
                                                      .sort(([, a], [, b]) => b.total_cost_usd - a.total_cost_usd)
                                                    // Show the breakdown whenever it has more than one tagged phase
                                                    // (e.g. several message_sequence items), or exactly one tagged
                                                    // phase that doesn't already account for the row's whole total
                                                    // (e.g. a reflection turn alongside untagged execution work) —
                                                    // comparing token counts rather than float cost to stay exact.
                                                    const taggedTokens = phaseEntries.reduce((sum, [, c]) => sum + totalTokens(c), 0)
                                                    const executionTokens = totalTokens(execution.cost)
                                                    const showPhaseBreakdown = phaseEntries.length > 1 || (phaseEntries.length === 1 && taggedTokens < executionTokens)
                                                    return (
                                                      <React.Fragment key={execution.id}>
                                                        <div className="flex items-center gap-2 border-b border-border/70 py-2 text-[10px] last:border-0" title={execution.id}>
                                                          <span className="min-w-0 flex-1 truncate text-foreground">{execution.label}</span>
                                                          <span className="shrink-0 font-mono text-muted-foreground">{tokenSummary(execution.cost)}</span>
                                                          <span className="shrink-0 font-mono text-muted-foreground">LLM time: {formatDuration(execution.cost.llm_generation_duration_ms)}</span>
                                                          <span className="shrink-0 font-mono text-foreground">{recordedCost(execution.cost)}</span>
                                                        </div>
                                                        {showPhaseBreakdown && phaseEntries.map(([phase, phaseCost]) => (
                                                          <div key={phase} className="flex items-center gap-2 border-b border-border/70 py-1 pl-4 text-[10px] text-muted-foreground last:border-0" title="Included in the total above">
                                                            <span className="min-w-0 flex-1 truncate">↳ {costPhaseLabel(phase)}</span>
                                                            <span className="shrink-0 font-mono">{tokenSummary(phaseCost)}</span>
                                                            <span className="shrink-0 font-mono">LLM time: {formatDuration(phaseCost.llm_generation_duration_ms)}</span>
                                                            <span className="shrink-0 font-mono">{recordedCost(phaseCost)}</span>
                                                          </div>
                                                        ))}
                                                      </React.Fragment>
                                                    )
                                                  })}
                                                </div>
                                              )}
                                            </div>
                                          )
                                        })}
                                      </div>
                                    )}
                                  </td>
                                </tr>
                              )}
                            </React.Fragment>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                  {costHistory?.hasMore && (
                    <div className="mt-4 flex justify-center">
                      <button
                        type="button"
                        onClick={loadOlderCosts}
                        disabled={loadingOlder}
                        className="inline-flex items-center gap-2 rounded-md border border-border px-3 py-2 text-xs font-medium text-foreground transition-colors hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        {loadingOlder && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                        {loadingOlder ? 'Loading older days…' : 'Load older days'}
                      </button>
                    </div>
                  )}
                </div>
              )}
  </>
)

export default CostsDailySection
