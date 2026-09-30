import React from 'react'
import {
  ChevronRight,
  ChevronDown,
  DollarSign,
  List,
  TrendingUp,
} from 'lucide-react'
import {
  formatUSD,
  formatTokens,
  getRunFolderDisplayName,
  getRunFolderSecondaryLabel,
  getRunFolderTitle,
  getRunBadgeLabel,
} from './helpers'
import StageCostCard from './StageCostCard'
import type { CostsData } from './useCostsData'

type RunCostsSectionProps = Pick<
  CostsData,
  | 'runCosts'
  | 'expandedRunFolders'
  | 'expandedCostModels'
  | 'costViewMode'
  | 'routeFilterByRunFolder'
  | 'toggleRunFolder'
  | 'toggleCostModel'
  | 'setViewModeForRunFolder'
  | 'setRouteFilterForRunFolder'
> & {
  selectedRunFolder: string | null
}

const RunCostsSection: React.FC<RunCostsSectionProps> = ({
  runCosts,
  selectedRunFolder,
  expandedRunFolders,
  expandedCostModels,
  costViewMode,
  routeFilterByRunFolder,
  toggleRunFolder,
  toggleCostModel,
  setViewModeForRunFolder,
  setRouteFilterForRunFolder,
}) => (
  <>
              {/* Per-run explorer complements the canonical activity ledger with
                  immutable webhook/run and step-level detail. */}
              {runCosts.length > 0 && (
                <div className="space-y-3">
                <div>
                  <h3 className="text-sm font-semibold text-foreground">Workflow runs</h3>
                  <p className="mt-1 text-xs text-muted-foreground">Open a run only when you need its step or model-level cost detail.</p>
                </div>
                {runCosts.map((runCost) => {
                  const isExpanded = expandedRunFolders.has(runCost.runFolder)
                  const viewMode = costViewMode[runCost.runFolder] || 'step'
                  const costSummary = runCost.costSummary
                  // step_id -> {stepNum, title}, built once per run instead of a
                  // linear scan of runCost.steps for every by_step_and_model key.
                  const stepInfoById = new Map<string, { stepNum: number; title: string }>()
                  for (const [key, stepData] of Object.entries(runCost.steps || {})) {
                    if (!stepData.step_id || stepInfoById.has(stepData.step_id)) continue
                    const match = key.match(/step-(\d+)/)
                    stepInfoById.set(stepData.step_id, {
                      stepNum: match ? parseInt(match[1], 10) : 0,
                      title: stepData.title || stepData.step_id,
                    })
                  }
                  const displayRunFolderName = getRunFolderDisplayName(runCost.runFolder)
                  const secondaryRunFolderLabel = getRunFolderSecondaryLabel(runCost)
                  const routeFilterKey = routeFilterByRunFolder[runCost.runFolder] || null
                  const routingRouteGroups: Array<{ key: string; routeStepTitle: string; routeName: string }> = []
                  if (costSummary) {
                    const seen = new Map<string, { key: string; routeStepTitle: string; routeName: string }>()
                    costSummary.stepCosts.forEach(step => {
                      if (!step.routeId || !step.routeStepId) return
                      const key = `${step.routeStepId}::${step.routeId}`
                      if (!seen.has(key)) {
                        seen.set(key, {
                          key,
                          routeStepTitle: step.routeStepTitle || step.routeStepId,
                          routeName: step.routeName || step.routeId,
                        })
                      }
                    })
                    routingRouteGroups.push(...seen.values())
                  }
                  const visibleStepCosts = costSummary
                    ? costSummary.stepCosts.filter(step => !routeFilterKey || `${step.routeStepId}::${step.routeId}` === routeFilterKey)
                    : []
                  // When a route filter is active, the totals row should sum
                  // only the visible (filtered) steps -- otherwise "Total"
                  // would silently include cost from routes the user just
                  // filtered out.
                  const totalsRowSource = routeFilterKey
                    ? visibleStepCosts.reduce((acc, step) => ({
                        tokens: acc.tokens + step.inputTokens + step.outputTokens,
                        inputTokens: acc.inputTokens + step.inputTokens,
                        outputTokens: acc.outputTokens + step.outputTokens,
                        execution: acc.execution + step.execution,
                        learning: acc.learning + step.learning,
                        knowledgebase: acc.knowledgebase + step.knowledgebase,
                        routing: acc.routing + step.routing,
                        workshop: acc.workshop + step.workshop,
                        evaluation: acc.evaluation + step.evaluation,
                        totalCost: acc.totalCost + step.totalCost,
                      }), { tokens: 0, inputTokens: 0, outputTokens: 0, execution: 0, learning: 0, knowledgebase: 0, routing: 0, workshop: 0, evaluation: 0, totalCost: 0 })
                    : costSummary
                      ? {
                          tokens: costSummary.totalTokens,
                          inputTokens: costSummary.totalInputTokens,
                          outputTokens: costSummary.totalOutputTokens,
                          execution: costSummary.stageCosts.execution,
                          learning: costSummary.stageCosts.learning,
                          knowledgebase: costSummary.stageCosts.knowledgebase,
                          routing: costSummary.stageCosts.routing,
                          workshop: costSummary.stageCosts.workshop,
                          evaluation: costSummary.stageCosts.evaluation,
                          totalCost: costSummary.totalCost,
                        }
                      : null

                  if (!costSummary) return null

                  return (
                    <div
                      key={runCost.runFolder}
                      className={`border rounded-lg overflow-hidden bg-card ${
                        runCost.runFolder === selectedRunFolder
                          ? 'border-purple-500/50 ring-1 ring-purple-500/20'
                          : 'border-border'
                      }`}
                    >
                      {/* Run Folder Header */}
                      <button
                        onClick={() => toggleRunFolder(runCost.runFolder)}
                        title={getRunFolderTitle(runCost)}
                        className={`w-full flex items-center justify-between px-4 py-3 text-left transition-colors ${
                          isExpanded ? 'bg-accent/50' : 'hover:bg-accent/50'
                        } ${runCost.runFolder === selectedRunFolder ? 'bg-purple-50/30 dark:bg-purple-900/10' : ''}`}
                      >
                        <div className="flex items-center gap-3 flex-1 min-w-0">
                          {isExpanded ? (
                            <ChevronDown className="w-4 h-4 text-muted-foreground flex-shrink-0" />
                          ) : (
                            <ChevronRight className="w-4 h-4 text-muted-foreground flex-shrink-0" />
                          )}
                          <div className="min-w-0">
                            <div className="flex items-center gap-2 min-w-0">
                              <span className={`font-mono text-xs px-1.5 py-0.5 rounded ${
                                runCost.runFolder === selectedRunFolder
                                  ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300 font-bold'
                                  : 'bg-muted text-foreground'
                              }`}>
                                {displayRunFolderName}
                              </span>
                              {runCost.runFolder === selectedRunFolder && (
                                <span className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded bg-purple-500 text-white shadow-sm">
                                  <TrendingUp className="w-2.5 h-2.5" />
                                  Current
                                </span>
                              )}
                            </div>
                            {secondaryRunFolderLabel && (
                              <div className="mt-1 text-[10px] text-muted-foreground truncate">
                                {secondaryRunFolderLabel}
                              </div>
                            )}
                          </div>
                        </div>

                        {/* Cost Badge */}
                        <div className="flex items-center gap-3 flex-shrink-0 ml-4">
                          <div className="flex items-center gap-2 px-3 py-1.5 rounded-full bg-green-100 dark:bg-green-900/30">
                            <span className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                              {getRunBadgeLabel(runCost)}
                            </span>
                            <DollarSign className="w-4 h-4 text-green-600 dark:text-green-400" />
                            <span className="text-sm font-semibold text-green-600 dark:text-green-400">
                              {formatUSD(costSummary.totalCost)}
                            </span>
                            <span className="text-xs text-muted-foreground">
                              ({formatTokens(costSummary.totalInputTokens)} input · {formatTokens(costSummary.totalOutputTokens)} output)
                            </span>
                          </div>
                        </div>
                      </button>

                      {/* Expanded Content */}
                      {isExpanded && (
                        <div className="border-t border-border p-4 space-y-4">
                          {/* Stage Summary Cards */}
                          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-7 gap-3">
                            <StageCostCard shadow label="Execution" value={costSummary.stageCosts.execution} />
                            <StageCostCard shadow label="Learning" value={costSummary.stageCosts.learning} />
                            <StageCostCard shadow label="Knowledgebase" value={costSummary.stageCosts.knowledgebase} />
                            <StageCostCard shadow label="Routing" value={costSummary.stageCosts.routing} />
                            <StageCostCard shadow label="Workshop" value={costSummary.stageCosts.workshop} />
                            <StageCostCard shadow label="Evaluation" value={costSummary.stageCosts.evaluation} />
                            <StageCostCard shadow label="Other" value={costSummary.stageCosts.other} />
                          </div>

                          {/* Cost Breakdown Table with View Toggle */}
                          {(runCost.tokenUsage?.by_model || runCost.tokenUsage?.by_tool || runCost.evaluationTokenUsage?.by_model || runCost.evaluationTokenUsage?.by_tool) && (
                            <div className="bg-card border border-border rounded-lg overflow-hidden shadow-sm">
                              <div className="px-4 py-3 bg-muted/30 border-b border-border flex items-center justify-between">
                                <h3 className="text-sm font-semibold flex items-center gap-2">
                                  <DollarSign className="w-4 h-4 text-green-500" />
                                  Cost Breakdown
                                </h3>
                                {/* View Toggle Buttons */}
                                <div className="flex items-center gap-1 bg-muted rounded-md p-0.5">
                                  <button
                                    onClick={() => setViewModeForRunFolder(runCost.runFolder, 'step')}
                                    className={`px-3 py-1 text-xs font-medium rounded transition-colors ${
                                      viewMode === 'step'
                                        ? 'bg-background text-foreground shadow-sm'
                                        : 'text-muted-foreground hover:text-foreground'
                                    }`}
                                  >
                                    By Step
                                  </button>
                                  <button
                                    onClick={() => setViewModeForRunFolder(runCost.runFolder, 'model')}
                                    className={`px-3 py-1 text-xs font-medium rounded transition-colors ${
                                      viewMode === 'model'
                                        ? 'bg-background text-foreground shadow-sm'
                                        : 'text-muted-foreground hover:text-foreground'
                                    }`}
                                  >
                                    By Model
                                  </button>
                                </div>
                              </div>

                              {/* Step-wise Cost Breakdown View */}
                              {viewMode === 'step' && costSummary.stepCosts.length > 0 && (
                                <div className="p-4 overflow-x-auto">
                                  {routingRouteGroups.length > 0 && (
                                    <div className="flex flex-wrap items-center gap-1.5 pb-3">
                                      <button
                                        type="button"
                                        onClick={() => setRouteFilterForRunFolder(runCost.runFolder, null)}
                                        className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium border transition-colors ${
                                          routeFilterKey === null
                                            ? 'bg-primary text-primary-foreground border-primary'
                                            : 'bg-muted text-muted-foreground border-border hover:bg-accent'
                                        }`}
                                      >
                                        All steps
                                      </button>
                                      {routingRouteGroups.map(group => (
                                        <button
                                          key={group.key}
                                          type="button"
                                          onClick={() => setRouteFilterForRunFolder(runCost.runFolder, group.key)}
                                          title={`Route "${group.routeName}" -- selected by ${group.routeStepTitle}`}
                                          className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium border transition-colors ${
                                            routeFilterKey === group.key
                                              ? 'bg-teal-600 text-white border-teal-600'
                                              : 'bg-muted text-muted-foreground border-border hover:bg-accent'
                                          }`}
                                        >
                                          {group.routeName}
                                        </button>
                                      ))}
                                    </div>
                                  )}
                                  <table className="w-full text-xs">
                                    <thead>
                                      <tr className="text-muted-foreground border-b border-border pb-2">
                                        <th className="text-left font-medium pb-2">Step</th>
                                        <th className="text-right font-medium pb-2">Input / output</th>
                                        <th className="text-right font-medium pb-2 text-blue-500">Execution</th>
                                        <th className="text-right font-medium pb-2 text-purple-500">Learning</th>
                                        <th className="text-right font-medium pb-2 text-teal-500">KB</th>
                                        <th className="text-right font-medium pb-2 text-cyan-500">Routing</th>
                                        <th className="text-right font-medium pb-2 text-pink-500">Workshop</th>
                                        <th className="text-right font-medium pb-2 text-amber-500">Evaluation</th>
                                        <th className="text-right font-medium pb-2">Total Cost</th>
                                      </tr>
                                    </thead>
                                    <tbody className="divide-y divide-border">
                                      {visibleStepCosts.map((step) => (
                                        <tr key={step.stepID} className="hover:bg-accent/50 transition-colors">
                                          <td className="py-2">
                                            <div className="font-medium text-foreground">
                                              {step.stepNum === 0 || step.stepNum > 1000
                                                ? (
                                                    <span className="flex items-center gap-1.5">
                                                      {step.stepTitle}
                                                      <span className="text-xs text-muted-foreground">
                                                        ({step.stepID})
                                                      </span>
                                                    </span>
                                                  )
                                                : (
                                                    <span>
                                                      Step {step.stepNum}: {step.stepTitle}
                                                      <span className="text-xs text-muted-foreground ml-1">
                                                        ({step.stepID})
                                                      </span>
                                                    </span>
                                                  )
                                              }
                                              {step.routeId && step.routeName && (
                                                <span
                                                  className="ml-1.5 inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-teal-500/10 text-teal-700 dark:text-teal-300 border border-teal-500/30"
                                                  title={`Reached via the "${step.routeName}" route, selected by ${step.routeStepTitle || step.routeStepId}`}
                                                >
                                                  ↳ {step.routeName}
                                                </span>
                                              )}
                                            </div>
                                          </td>
                                          <td className="py-2 text-right font-mono text-muted-foreground">
                                            {formatTokens(step.inputTokens)} input · {formatTokens(step.outputTokens)} output
                                          </td>
                                          <td className="py-2 text-right font-mono text-blue-600 dark:text-blue-400">
                                            {formatUSD(step.execution)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-purple-600 dark:text-purple-400">
                                            {formatUSD(step.learning)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-teal-600 dark:text-teal-400">
                                            {formatUSD(step.knowledgebase)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-cyan-600 dark:text-cyan-400">
                                            {formatUSD(step.routing)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-pink-600 dark:text-pink-400">
                                            {formatUSD(step.workshop)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-amber-600 dark:text-amber-400">
                                            {formatUSD(step.evaluation)}
                                          </td>
                                          <td className="py-2 text-right font-bold text-foreground">
                                            {formatUSD(step.totalCost)}
                                          </td>
                                        </tr>
                                      ))}
                                      {/* Total Row -- subtotal for the selected route when a route filter is active */}
                                      {totalsRowSource && (
                                        <tr className="bg-muted/30 font-semibold">
                                          <td className="py-2 text-foreground">{routeFilterKey ? 'Total (this route)' : 'Total'}</td>
                                          <td className="py-2 text-right font-mono text-muted-foreground">
                                            {formatTokens(totalsRowSource.inputTokens)} input · {formatTokens(totalsRowSource.outputTokens)} output
                                          </td>
                                          <td className="py-2 text-right font-mono text-blue-600 dark:text-blue-400">
                                            {formatUSD(totalsRowSource.execution)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-purple-600 dark:text-purple-400">
                                            {formatUSD(totalsRowSource.learning)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-teal-600 dark:text-teal-400">
                                            {formatUSD(totalsRowSource.knowledgebase)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-cyan-600 dark:text-cyan-400">
                                            {formatUSD(totalsRowSource.routing)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-pink-600 dark:text-pink-400">
                                            {formatUSD(totalsRowSource.workshop)}
                                          </td>
                                          <td className="py-2 text-right font-mono text-amber-600 dark:text-amber-400">
                                            {formatUSD(totalsRowSource.evaluation)}
                                          </td>
                                          <td className="py-2 text-right font-bold text-green-600 dark:text-green-400">
                                            {formatUSD(totalsRowSource.totalCost)}
                                          </td>
                                        </tr>
                                      )}
                                    </tbody>
                                  </table>
                                </div>
                              )}

                              {/* Model-wise Cost Breakdown View */}
                              {viewMode === 'model' && (runCost.tokenUsage || runCost.evaluationTokenUsage) && (
                                <div className="p-4 overflow-x-auto">
                                  <table className="w-full text-xs">
                                    <thead>
                                      <tr className="text-muted-foreground border-b border-border pb-2">
                                        <th className="w-8"></th>
                                        <th className="text-left font-medium pb-2">Model</th>
                                        <th className="text-right font-medium pb-2">Input</th>
                                        <th className="text-right font-medium pb-2">Cached In</th>
                                        <th className="text-right font-medium pb-2">Cache Write</th>
                                        <th className="text-right font-medium pb-2">Reasoning</th>
                                        <th className="text-right font-medium pb-2">Output</th>
                                        <th className="text-right font-medium pb-2">Cost (USD)</th>
                                      </tr>
                                    </thead>
                                    <tbody className="divide-y divide-border">
                                      {runCost.tokenUsage && Object.entries(runCost.tokenUsage.by_model || {}).map(([modelId, usage]) => {
                                        const cacheRead = usage.cache_read_tokens || usage.cache_tokens || 0
                                        const cacheWrite = usage.cache_write_tokens || 0
                                        const reasoning = usage.reasoning_tokens || 0
                                        const cachePercent = usage.input_tokens > 0 ? (cacheRead / usage.input_tokens) * 100 : 0
                                        const modelKey = `${runCost.runFolder}-${modelId}`
                                        const isModelExpanded = expandedCostModels.has(modelKey)

                                        // Calculate step-wise breakdown for this model -- only rendered
                                        // when the model row is expanded, so only computed then.
                                        const modelSteps = isModelExpanded && runCost.tokenUsage && runCost.tokenUsage.by_step_and_model
                                          ? Object.entries(runCost.tokenUsage.by_step_and_model)
                                              .map(([stepKey, modelMap]) => {
                                                const stepUsage = modelMap[modelId]
                                                if (!stepUsage) return null

                                                const parts = stepKey.split(':')
                                                const phase = parts[0]
                                                const stepID = parts[1] || ''  // New format: stepID instead of index

                                                let phaseLabel = ''
                                                if (phase === 'execution_only') { phaseLabel = 'Execution' }
                                                else if (phase.includes('learning')) { phaseLabel = 'Learning' }
                                                else if (phase.startsWith('kb_')) { phaseLabel = 'Knowledgebase' }
                                                else if (phase === 'routing' || phase === 'todo_task') { phaseLabel = 'Routing' }
                                                else if (phase === 'review_step_code' || phase === 'goal_advisor' || phase === 'plan_change' || phase === 'replan_workflow_from_results') { phaseLabel = 'Workshop' }
                                                else if (phase === 'evaluation_scoring' || phase.startsWith('evaluation')) { phaseLabel = 'Evaluation' }
                                                else { phaseLabel = phase }

                                                // Try to find step info from stepID
                                                const stepInfo = stepInfoById.get(stepID)
                                                const stepNum = stepInfo?.stepNum ?? 0
                                                const stepTitle = stepInfo?.title ?? stepID

                                                let label = ''
                                                if (stepNum > 0) {
                                                  label = `Step ${stepNum}: ${stepTitle} (${phaseLabel})`
                                                } else {
                                                  // Phase-only agent
                                                  label = `${stepTitle} (${phaseLabel})`
                                                }

                                                return { key: stepKey, label, usage: stepUsage, stepNum }
                                              })
                                              .filter((s): s is NonNullable<typeof s> => s !== null)
                                              .sort((a, b) => {
                                                // Sort by step number first, then by label
                                                if (a.stepNum !== b.stepNum) return a.stepNum - b.stepNum
                                                return a.label.localeCompare(b.label)
                                              })
                                          : []

                                        return (
                                          <React.Fragment key={modelId}>
                                            <tr className="hover:bg-accent/50 transition-colors cursor-pointer" onClick={() => toggleCostModel(modelKey)}>
                                              <td className="py-2 pl-2">
                                                {isModelExpanded ? (
                                                  <ChevronDown className="w-3 h-3 text-muted-foreground" />
                                                ) : (
                                                  <ChevronRight className="w-3 h-3 text-muted-foreground" />
                                                )}
                                              </td>
                                              <td className="py-2">
                                                <div className="font-mono text-foreground font-medium">{modelId}</div>
                                                <div className="text-[10px] text-muted-foreground uppercase">{usage.provider}</div>
                                              </td>
                                              <td className="py-2 text-right text-muted-foreground">{usage.input_tokens.toLocaleString()}</td>
                                              <td className="py-2 text-right">
                                                <div className="text-foreground">{cacheRead.toLocaleString()}</div>
                                                {cachePercent > 0 && (
                                                  <div className="text-[10px] text-green-600 dark:text-green-400">({cachePercent.toFixed(0)}%)</div>
                                                )}
                                              </td>
                                              <td className="py-2 text-right text-muted-foreground">{cacheWrite > 0 ? cacheWrite.toLocaleString() : '-'}</td>
                                              <td className="py-2 text-right text-muted-foreground">{reasoning > 0 ? reasoning.toLocaleString() : '-'}</td>
                                              <td className="py-2 text-right text-muted-foreground">{usage.output_tokens.toLocaleString()}</td>
                                              <td className="py-2 text-right text-green-600 dark:text-green-400 font-semibold">{formatUSD(usage.total_cost_usd)}</td>
                                            </tr>
                                            {isModelExpanded && modelSteps.length > 0 && (
                                              <tr className="bg-muted/20">
                                                <td colSpan={8} className="p-0">
                                                  <div className="p-4 space-y-4">
                                                    <div className="border border-border rounded-md overflow-hidden bg-background">
                                                      <div className="bg-muted/50 px-4 py-2 border-b border-border flex justify-between items-center">
                                                        <h4 className="font-semibold text-xs text-foreground flex items-center gap-2">
                                                          <List className="w-3.5 h-3.5" /> Usage by Step
                                                        </h4>
                                                      </div>
                                                      <div className="overflow-x-auto">
                                                        <table className="w-full text-xs">
                                                          <thead>
                                                            <tr className="text-muted-foreground border-b border-border bg-muted/30">
                                                              <th className="px-4 py-2 text-left font-medium">Step</th>
                                                              <th className="px-4 py-2 text-right font-medium">Input</th>
                                                              <th className="px-4 py-2 text-right font-medium">Cached In</th>
                                                              <th className="px-4 py-2 text-right font-medium">Reasoning</th>
                                                              <th className="px-4 py-2 text-right font-medium">Output</th>
                                                              <th className="px-4 py-2 text-right font-medium">Cost</th>
                                                            </tr>
                                                          </thead>
                                                          <tbody className="divide-y divide-border">
                                                            {modelSteps.map((step) => (
                                                              <tr key={step.key} className="hover:bg-muted/30 transition-colors">
                                                                <td className="px-4 py-2">
                                                                  <span className="font-medium text-foreground">{step.label}</span>
                                                                </td>
                                                                <td className="px-4 py-2 text-right text-muted-foreground">{step.usage.input_tokens.toLocaleString()}</td>
                                                                <td className="px-4 py-2 text-right text-muted-foreground">
                                                                  {(step.usage.cache_read_tokens || step.usage.cache_tokens || 0).toLocaleString()}
                                                                </td>
                                                                <td className="px-4 py-2 text-right text-muted-foreground">
                                                                  {(step.usage.reasoning_tokens || 0).toLocaleString()}
                                                                </td>
                                                                <td className="px-4 py-2 text-right text-muted-foreground">{step.usage.output_tokens.toLocaleString()}</td>
                                                                <td className="px-4 py-2 text-right text-green-600 dark:text-green-400 font-medium">{formatUSD(step.usage.total_cost_usd)}</td>
                                                              </tr>
                                                            ))}
                                                          </tbody>
                                                        </table>
                                                      </div>
                                                    </div>
                                                  </div>
                                                </td>
                                              </tr>
                                            )}
                                          </React.Fragment>
                                        )
                                      })}
                                      {runCost.tokenUsage && Object.entries(runCost.tokenUsage.by_tool || {}).map(([toolId, usage]) => (
                                        <tr key={`tool-${toolId}`} className="hover:bg-accent/50 transition-colors">
                                          <td className="py-2 pl-2"></td>
                                          <td className="py-2">
                                            <div className="font-mono text-foreground font-medium">{usage.tool_name || toolId}</div>
                                            <div className="text-[10px] text-muted-foreground uppercase">
                                              {[usage.provider, usage.model_id, usage.estimated ? 'estimated' : ''].filter(Boolean).join(' | ')}
                                            </div>
                                          </td>
                                          <td className="py-2 text-right text-muted-foreground" colSpan={5}>
                                            {usage.quantity ? `${usage.quantity.toFixed(4)} ${usage.unit || ''}` : (usage.unit || 'tool usage')}
                                          </td>
                                          <td className="py-2 text-right text-green-600 dark:text-green-400 font-semibold">{formatUSD(usage.total_cost_usd)}</td>
                                        </tr>
                                      ))}
                                      {runCost.evaluationTokenUsage && Object.entries(runCost.evaluationTokenUsage.by_tool || {}).map(([toolId, usage]) => (
                                        <tr key={`eval-tool-${toolId}`} className="hover:bg-accent/50 transition-colors">
                                          <td className="py-2 pl-2"></td>
                                          <td className="py-2">
                                            <div className="font-mono text-foreground font-medium">{usage.tool_name || toolId}</div>
                                            <div className="text-[10px] text-muted-foreground uppercase">
                                              {['evaluation', usage.provider, usage.model_id, usage.estimated ? 'estimated' : ''].filter(Boolean).join(' | ')}
                                            </div>
                                          </td>
                                          <td className="py-2 text-right text-muted-foreground" colSpan={5}>
                                            {usage.quantity ? `${usage.quantity.toFixed(4)} ${usage.unit || ''}` : (usage.unit || 'tool usage')}
                                          </td>
                                          <td className="py-2 text-right text-green-600 dark:text-green-400 font-semibold">{formatUSD(usage.total_cost_usd)}</td>
                                        </tr>
                                      ))}
                                    </tbody>
                                    <tfoot>
                                      <tr className="border-t-2 border-border font-bold">
                                        <td></td>
                                        <td className="py-3 text-foreground">Total Summary</td>
                                        <td className="py-3 text-right text-muted-foreground">{costSummary.totalInputTokens.toLocaleString()}</td>
                                        <td className="py-3 text-right text-muted-foreground">
                                          {costSummary.totalCacheReadTokens.toLocaleString()}
                                          {costSummary.totalInputTokens > 0 && (
                                            <span className="text-[10px] text-muted-foreground ml-1">
                                              ({((costSummary.totalCacheReadTokens / costSummary.totalInputTokens) * 100).toFixed(0)}%)
                                            </span>
                                          )}
                                        </td>
                                        <td className="py-3 text-right text-muted-foreground">{costSummary.totalCacheWriteTokens.toLocaleString()}</td>
                                        <td className="py-3 text-right text-muted-foreground">{costSummary.totalReasoningTokens.toLocaleString()}</td>
                                        <td className="py-3 text-right text-muted-foreground">{costSummary.totalOutputTokens.toLocaleString()}</td>
                                        <td className="py-3 text-right text-green-600 dark:text-green-400">{formatUSD(costSummary.totalCost)}</td>
                                      </tr>
                                    </tfoot>
                                  </table>
                                </div>
                              )}
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
                </div>
              )}
  </>
)

export default RunCostsSection
