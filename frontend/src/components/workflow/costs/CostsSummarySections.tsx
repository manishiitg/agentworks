import React from 'react'
import { DollarSign, Award, TrendingUp, TrendingDown } from 'lucide-react'
import { formatUSD, formatTokens, formatTimestampLabel } from './helpers'
import StageCostCard from './StageCostCard'
import type { CostsData } from './useCostsData'

type CostsSummarySectionsProps = Pick<
  CostsData,
  'hasScopedActivity' | 'phaseCostSummary' | 'phaseDailyCostSummaries' | 'aggregateSummary'
>

const CostsSummarySections: React.FC<CostsSummarySectionsProps> = ({
  hasScopedActivity,
  phaseCostSummary,
  phaseDailyCostSummaries,
  aggregateSummary,
}) => (
  <>
              {/* Automation Builder / Phase Costs */}
              {!hasScopedActivity && phaseCostSummary && (
                <div className="bg-card border border-border rounded-lg p-4 shadow-sm">
                  <div className="flex items-start justify-between gap-4 mb-4">
                    <div>
                      <h3 className="text-sm font-semibold text-foreground mb-1 flex items-center gap-2">
                        <DollarSign className="w-4 h-4 text-amber-500" />
                        Automation Builder Costs
                      </h3>
                      {phaseCostSummary.updatedAt && (
                        <p className="text-[10px] text-muted-foreground mt-1">
                          Last updated: {formatTimestampLabel(phaseCostSummary.updatedAt)}
                        </p>
                      )}
                    </div>
                  </div>

                  <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                    <div className="bg-amber-100 dark:bg-amber-900/30 rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1">Builder Total</div>
                      <div className="text-2xl font-bold text-amber-600 dark:text-amber-400">
                        {formatUSD(phaseCostSummary.totalCost)}
                      </div>
                      <div className="text-xs text-muted-foreground mt-1">
                        {formatTokens(phaseCostSummary.totalInputTokens)} input · {formatTokens(phaseCostSummary.totalOutputTokens)} output
                      </div>
                    </div>

                    <div className="bg-card border border-border rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1">Input tokens</div>
                      <div className="text-2xl font-bold text-foreground">
                        {formatTokens(phaseCostSummary.totalInputTokens)}
                      </div>
                    </div>

                    <div className="bg-card border border-border rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1">Output tokens</div>
                      <div className="text-2xl font-bold text-foreground">
                        {formatTokens(phaseCostSummary.totalOutputTokens)}
                      </div>
                    </div>

                    <div className="bg-card border border-border rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1">Tracked Phases</div>
                      <div className="text-2xl font-bold text-foreground">
                        {phaseCostSummary.phaseCosts.length}
                      </div>
                    </div>
                  </div>

                  {phaseCostSummary.phaseCosts.length > 0 && (
                    <div className="mt-4 overflow-x-auto">
                      <table className="w-full text-xs">
                        <thead>
                          <tr className="text-muted-foreground border-b border-border pb-2">
                            <th className="text-left font-medium pb-2">Phase</th>
                            <th className="text-right font-medium pb-2">Input tokens</th>
                            <th className="text-right font-medium pb-2">Output tokens</th>
                            <th className="text-right font-medium pb-2">Cost</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-border">
                          {phaseCostSummary.phaseCosts.map(phase => (
                            <tr key={phase.phaseID} className="hover:bg-accent/50 transition-colors">
                              <td className="py-2">
                                <div className="font-medium text-foreground">{phase.phaseTitle}</div>
                                <div className="text-[10px] text-muted-foreground font-mono">{phase.phaseID}</div>
                              </td>
                              <td className="py-2 text-right font-mono text-muted-foreground">
                                {phase.inputTokens.toLocaleString()}
                              </td>
                              <td className="py-2 text-right font-mono text-muted-foreground">
                                {phase.outputTokens.toLocaleString()}
                              </td>
                              <td className="py-2 text-right font-bold text-amber-600 dark:text-amber-400">
                                {formatUSD(phase.totalCost)}
                              </td>
                            </tr>
                          ))}
                          <tr className="bg-muted/30 font-semibold">
                            <td className="py-2 text-foreground">Total</td>
                            <td className="py-2 text-right font-mono text-muted-foreground">
                              {phaseCostSummary.totalInputTokens.toLocaleString()}
                            </td>
                            <td className="py-2 text-right font-mono text-muted-foreground">
                              {phaseCostSummary.totalOutputTokens.toLocaleString()}
                            </td>
                            <td className="py-2 text-right font-bold text-amber-600 dark:text-amber-400">
                              {formatUSD(phaseCostSummary.totalCost)}
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  )}

                  {phaseCostSummary.modelCosts.length > 0 && (
                    <div className="mt-5 overflow-x-auto">
                      <div className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                        LLM Breakdown
                      </div>
                      <table className="w-full text-xs">
                        <thead>
                          <tr className="text-muted-foreground border-b border-border pb-2">
                            <th className="text-left font-medium pb-2">Model</th>
                            <th className="text-right font-medium pb-2">Provider</th>
                            <th className="text-right font-medium pb-2">Input tokens</th>
                            <th className="text-right font-medium pb-2">Output tokens</th>
                            <th className="text-right font-medium pb-2">Cost</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-border">
                          {phaseCostSummary.modelCosts.map(model => (
                            <tr key={model.modelID} className="hover:bg-accent/50 transition-colors">
                              <td className="py-2">
                                <div className="font-medium text-foreground font-mono">{model.modelID}</div>
                              </td>
                              <td className="py-2 text-right text-muted-foreground">
                                {model.provider}
                              </td>
                              <td className="py-2 text-right font-mono text-muted-foreground">
                                {model.inputTokens.toLocaleString()}
                              </td>
                              <td className="py-2 text-right font-mono text-muted-foreground">
                                {model.outputTokens.toLocaleString()}
                              </td>
                              <td className="py-2 text-right font-bold text-amber-600 dark:text-amber-400">
                                {formatUSD(model.totalCost)}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}

                  {phaseDailyCostSummaries.length === 0 && (
                    <p className="mt-5 text-xs text-muted-foreground">
                      Daily history unavailable.
                    </p>
                  )}
                </div>
              )}

              {/* Aggregate Summary */}
              {!hasScopedActivity && aggregateSummary && (
                <div className="bg-card border border-border rounded-lg p-4 shadow-sm">
                  <h3 className="text-sm font-semibold text-foreground mb-4 flex items-center gap-2">
                    <Award className="w-4 h-4 text-primary" />
                    Aggregate Summary ({aggregateSummary.totalRuns} runs)
                  </h3>
                  <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                    {/* Total Cost */}
                    <div className="bg-green-100 dark:bg-green-900/30 rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1">Total Cost</div>
                      <div className="text-2xl font-bold text-green-600 dark:text-green-400">
                        {formatUSD(aggregateSummary.totalCost)}
                      </div>
                      <div className="text-xs text-muted-foreground mt-1">
                        {formatTokens(aggregateSummary.totalInputTokens)} input · {formatTokens(aggregateSummary.totalOutputTokens)} output
                      </div>
                    </div>

                    {/* Highest Cost */}
                    <div className="bg-blue-100 dark:bg-blue-900/30 rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1 flex items-center gap-1">
                        <TrendingUp className="w-3 h-3" />
                        Highest
                      </div>
                      <div className="text-2xl font-bold text-blue-600 dark:text-blue-400">
                        {formatUSD(aggregateSummary.highestCost)}
                      </div>
                    </div>

                    {/* Lowest Cost */}
                    <div className="bg-purple-100 dark:bg-purple-900/30 rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1 flex items-center gap-1">
                        <TrendingDown className="w-3 h-3" />
                        Lowest
                      </div>
                      <div className="text-2xl font-bold text-purple-600 dark:text-purple-400">
                        {formatUSD(aggregateSummary.lowestCost)}
                      </div>
                    </div>

                    {/* Total Runs */}
                    <div className="bg-muted rounded-lg p-3">
                      <div className="text-xs text-muted-foreground uppercase tracking-wider mb-1">Runs</div>
                      <div className="text-2xl font-bold text-foreground">
                        {aggregateSummary.totalRuns}
                      </div>
                    </div>
                  </div>

                  {/* Stage Costs Summary */}
                  <div className="mt-4 grid grid-cols-2 md:grid-cols-4 lg:grid-cols-8 gap-3">
                    <StageCostCard label="Execution" value={aggregateSummary.stageCosts.execution} />
                    <StageCostCard label="Learning" value={aggregateSummary.stageCosts.learning} />
                    <StageCostCard label="Reflection" value={aggregateSummary.stageCosts.reflection} />
                    <StageCostCard label="Knowledgebase" value={aggregateSummary.stageCosts.knowledgebase} />
                    <StageCostCard label="Routing" value={aggregateSummary.stageCosts.routing} />
                    <StageCostCard label="Workshop" value={aggregateSummary.stageCosts.workshop} />
                    <StageCostCard label="Evaluation" value={aggregateSummary.stageCosts.evaluation} />
                    <StageCostCard label="Other" value={aggregateSummary.stageCosts.other} />
                  </div>
                </div>
              )}
  </>
)

export default CostsSummarySections
