import { useMemo } from 'react'
import type React from 'react'
import {
  ChevronRight,
  ChevronDown,
  Clock,
} from 'lucide-react'
import type { ExecutionLogsResponse, StepExecutionLogs } from '../../../services/api-types'
import { StepMetricChip } from './LogPrimitives'
import { AskAIButton } from '../AskAIButton'
import { buildStepAskMessage } from './logsAskAI'
import { formatDuration, formatRunFolderLabel, getStepDisplayTitle, getStepIcon, getRecordedStepType, getStepIndentStyle, getStepLatestError, getStepMetrics, getStepModel, getStepNestingClass, getStepNestingLevel, getStepResultPreview, getStepStatus, getStepTypeBadgeStyle, getStepTypeDescription, getStepTypeLabel, hasKnowledgebaseSignal, hasLearningSignal, hasStepMetrics, sortStepEntriesByExecution } from './helpers'
import { formatTokens } from '../../../utils/formatTokens'

export interface StepAskContext {
  workspacePath: string | null
  runFolder: string
}

export interface StepListProps {
  logs: ExecutionLogsResponse | null
  focusedStepId: string | undefined
  routeFilterKey: string | null
  showFailedOnly?: boolean
  expandedSteps: Set<string>
  toggleStep: (stepId: string) => void
  renderStepContent: (stepId: string, stepLogs: StepExecutionLogs) => React.ReactNode
  askContext?: StepAskContext
}

const stepStatusPill = (status: ReturnType<typeof getStepStatus>): { label: string; className: string; pulse: boolean } => {
  switch (status) {
    case 'running':
      return { label: 'Running', className: 'border-sky-500/35 bg-sky-500/10 text-sky-700 dark:text-sky-300', pulse: true }
    case 'completed':
      return { label: 'Completed', className: 'border-emerald-500/35 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300', pulse: false }
    case 'failed':
      return { label: 'Failed run', className: 'border-destructive/45 bg-destructive/10 text-destructive', pulse: false }
    default:
      return { label: 'Not run', className: 'border-border bg-muted text-muted-foreground', pulse: false }
  }
}

export function StepList({ logs, focusedStepId, routeFilterKey, showFailedOnly = false, expandedSteps, toggleStep, renderStepContent, askContext }: StepListProps) {
  // The sort comparator walks every execution of both steps per comparison;
  // do it once per logs payload, not on every 2.5 s poll re-render.
  const sortedStepEntries = useMemo(
    () => Object.entries(logs?.steps || {}).sort(sortStepEntriesByExecution),
    [logs],
  )
  return (
    <>
              {sortedStepEntries
                .filter(([stepId]) => !focusedStepId || stepId === focusedStepId)
                .filter(([, stepLogs]) =>
                  !routeFilterKey ||
                  (stepLogs.route_kind === 'routing' && `${stepLogs.route_step_id}::${stepLogs.route_id}` === routeFilterKey)
                )
                .filter(([, stepLogs]) => !showFailedOnly || getStepStatus(stepLogs) === 'failed')
                .map(([stepId, stepLogs]) => {
                  const isExpanded = expandedSteps.has(stepId)
                  const displayTitle = getStepDisplayTitle(stepId, stepLogs)
                  const resultPreview = getStepResultPreview(stepLogs)
                  const nestingLevel = getStepNestingLevel(stepId)
                  const indentStyle = getStepIndentStyle(nestingLevel)
                  const nestingClass = getStepNestingClass(stepId)
                  const stepMetrics = getStepMetrics(stepLogs.executions || [])
                  const showMetrics = hasStepMetrics(stepMetrics)
                  const stepModel = getStepModel(stepLogs.executions || [])

                  const stepStatus = getStepStatus(stepLogs)
                  const pill = stepStatusPill(stepStatus)
                  const latestError = getStepLatestError(stepLogs)
                  const excerpt = stepStatus === 'failed' && latestError ? latestError : resultPreview

                  return (
                    <div key={stepId} className={`relative rounded-lg border border-border bg-card overflow-hidden transition-colors ${nestingClass}`} style={indentStyle}>
                      <div className="flex items-start">
                        <button
                          onClick={() => toggleStep(stepId)}
                          aria-expanded={isExpanded}
                          aria-controls={`execution-step-${stepId}`}
                          aria-label={`${isExpanded ? 'Collapse' : 'Expand'} ${displayTitle}`}
                          className={`
                            min-w-0 flex-1 flex flex-col gap-2 px-4 py-3 text-left transition-colors
                            ${isExpanded ? 'bg-accent/30' : 'hover:bg-accent/40'}
                          `}
                        >
                          <div className="flex w-full items-start justify-between gap-3">
                            <div className="flex min-w-0 items-start gap-3 overflow-hidden flex-1">
                              {isExpanded ? <ChevronDown className="w-4 h-4 text-muted-foreground flex-shrink-0 mt-0.5" /> : <ChevronRight className="w-4 h-4 text-muted-foreground flex-shrink-0 mt-0.5" />}

                              <div className="flex flex-col items-start text-left min-w-0">
                                <div className="flex items-center gap-2 flex-wrap">
                                  <span className="flex-shrink-0" title={getStepTypeDescription(getRecordedStepType(stepLogs) || 'scripted')}>
                                    {getStepIcon(getRecordedStepType(stepLogs))}
                                  </span>
                                  <span className="text-sm font-semibold text-foreground truncate">{displayTitle}</span>
                                  <span title={getStepTypeDescription(getRecordedStepType(stepLogs) || 'scripted')} className={`inline-flex items-center px-1.5 py-0.5 rounded-md text-[10px] font-medium border ${getStepTypeBadgeStyle(getRecordedStepType(stepLogs))}`}>
                                    {getStepTypeLabel(getRecordedStepType(stepLogs))}
                                  </span>
                                  <span className={`inline-flex shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-semibold ${pill.className}`}>
                                    {pill.pulse && <span className="h-1.5 w-1.5 rounded-full bg-current motion-safe:animate-pulse" />}
                                    {pill.label}
                                  </span>
                                </div>
                                {excerpt && (
                                  <span className="mt-0.5 w-full truncate pl-6 text-xs text-muted-foreground" title={excerpt}>{excerpt}</span>
                                )}
                              </div>
                            </div>
                          </div>

                          <div className="flex w-full flex-wrap items-center gap-1.5 pl-11 text-xs text-muted-foreground">
                            {stepModel && (
                              <StepMetricChip title={`Model used on the most recent attempt: ${stepModel}`}>
                                {stepModel}
                              </StepMetricChip>
                            )}
                            {showMetrics && stepMetrics.totalTokens > 0 && (
                              <StepMetricChip title={`Tokens used: ${stepMetrics.totalTokens.toLocaleString()} total (${stepMetrics.inputTokens.toLocaleString()} input, ${stepMetrics.outputTokens.toLocaleString()} output${stepMetrics.reasoningTokens > 0 ? `, ${stepMetrics.reasoningTokens.toLocaleString()} reasoning` : ''}${stepMetrics.cacheTokens > 0 ? `, ${stepMetrics.cacheTokens.toLocaleString()} cache` : ''})`}>
                                {formatTokens(stepMetrics.totalTokens)} tok total
                              </StepMetricChip>
                            )}
                            {showMetrics && stepMetrics.durationMs > 0 && (
                              <StepMetricChip title={`Time taken: ${formatDuration(stepMetrics.durationMs)}${stepMetrics.llmCalls > 0 ? ` across ${stepMetrics.llmCalls} LLM call${stepMetrics.llmCalls !== 1 ? 's' : ''}` : ''}`}>
                                <Clock className="h-3 w-3" />
                                {formatDuration(stepMetrics.durationMs)}
                              </StepMetricChip>
                            )}
                            <span className="whitespace-nowrap">
                              {stepLogs.executions.length} exec
                              {hasLearningSignal(stepLogs) && ' • learning'}
                              {hasKnowledgebaseSignal(stepLogs) && ' • kb'}
                              {stepLogs.todo_task && stepLogs.todo_task.length > 0 && ` • ${stepLogs.todo_task.length} todo`}
                            </span>
                          </div>
                        </button>
                        {askContext && (
                          <div className="shrink-0 px-2 py-2.5">
                            <AskAIButton
                              workspacePath={askContext.workspacePath}
                              iconOnly
                              message={buildStepAskMessage({
                                runFolder: askContext.runFolder,
                                folderLabel: formatRunFolderLabel(askContext.runFolder),
                                stepTitle: displayTitle,
                                status: stepStatus,
                                duration: showMetrics && stepMetrics.durationMs > 0 ? formatDuration(stepMetrics.durationMs) : undefined,
                                tokens: showMetrics && stepMetrics.totalTokens > 0 ? `${formatTokens(stepMetrics.totalTokens)} tokens` : undefined,
                                preview: resultPreview || undefined,
                                error: latestError || undefined,
                              })}
                            />
                          </div>
                        )}
                      </div>

                      {isExpanded && (
                        <div id={`execution-step-${stepId}`}>
                          {renderStepContent(stepId, stepLogs)}
                        </div>
                      )}
                    </div>
                  )
                })}
    </>
  )
}
