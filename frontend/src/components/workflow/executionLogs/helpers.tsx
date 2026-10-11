import type React from 'react'
import {
  MessageSquare,
  Network,
  Bot,
  User,
  ListTodo,
  FileText,
} from 'lucide-react'
import type { StepExecutionLogs } from '../../../services/api-types'

export interface ValidationFeedback {
  severity: 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | string
  description: string
}

const ITERATION_ZERO_DEFAULT_FOLDER = 'iteration-0/default'

const isIterationZeroRunFolder = (folder: string) => (
  folder === 'iteration-0' || folder.startsWith('iteration-0/')
)

export const isWebhookRunFolder = (folder: string | null | undefined) => (
  /^iteration-\d+-hook(?:\/|$)/.test((folder || '').trim())
)

export const getDefaultRunFolder = (initialRunFolder: string | null | undefined, runFolders: string[]) => {
  if (initialRunFolder && initialRunFolder !== 'new') return initialRunFolder
  const groupedRunFolder = runFolders.find(folder => folder.includes('/'))
  if (groupedRunFolder) return groupedRunFolder
  const iterationZeroFolder = runFolders.find(isIterationZeroRunFolder)
  if (iterationZeroFolder) return iterationZeroFolder
  return ITERATION_ZERO_DEFAULT_FOLDER
}

export const formatLogFileContent = (content: unknown): string => {
  if (typeof content !== 'string') return JSON.stringify(content, null, 2)
  const trimmed = content.trim()
  if (!trimmed || (!trimmed.startsWith('{') && !trimmed.startsWith('['))) return content

  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2)
  } catch {
    return content
  }
}

const parseJsonLike = (content: unknown): unknown => {
  if (typeof content !== 'string') return content
  const trimmed = content.trim()
  if (!trimmed || (!trimmed.startsWith('{') && !trimmed.startsWith('['))) return content

  try {
    return JSON.parse(trimmed)
  } catch {
    return content
  }
}

export const getStepResultPreview = (stepLogs: unknown): string => {
  const step = asRecord(stepLogs)
  const executions = Array.isArray(step?.executions) ? step.executions : []

  for (let index = executions.length - 1; index >= 0; index -= 1) {
    const execution = asRecord(executions[index])
    const content = asRecord(execution?.content)
    const candidates = [
      content?.execution_result,
      content?.result,
      content?.output,
      execution?.result,
    ]

    for (const candidate of candidates) {
      if (typeof candidate !== 'string') continue
      const compact = candidate.replace(/\s+/g, ' ').trim()
      if (compact) return compact
    }
  }

  return ''
}

type ExecutionOrigin = {
  label: string
  detail: string
  className: string
  plannedMessage?: string
}

// A agent execution can be a planned item, a repair injected by
// automatic final validation, or a reflection. Make that lifecycle visible so
// "4 attempts" is not mistaken for four orchestrator dispatches.
export const getExecutionOrigin = (execution: unknown, validations: unknown[], plannedMessages: unknown[] = []): ExecutionOrigin => {
  const exec = asRecord(execution)
  const content = asRecord(exec?.content)
  const result = typeof content?.execution_result === 'string' ? content.execution_result : ''
  const itemMatch = result.match(/^Agent item:\s*([^\s(]+)\s*\(/m)
  const itemID = itemMatch?.[1] || ''
  const plannedItem = plannedMessages
    .map(asRecord)
    .find(item => item?.id === itemID)
  const plannedMessage = typeof plannedItem?.message === 'string' ? plannedItem.message : undefined
  const repairMatch = itemID.match(/^__automatic_final_validation__-repair-(\d+)$/)

  if (repairMatch) {
    const validationAttempt = Number(repairMatch[1])
    const trigger = validations
      .map(asRecord)
      .find(validation => (
        validation?.kind === 'pre_validation' &&
        Number(asRecord(validation.content)?.validation_attempt) === validationAttempt
      ))
    const validationContent = asRecord(trigger?.content)
    const failedChecks = Number(validationContent?.failed_checks)
    const errors = Array.isArray(validationContent?.errors) ? validationContent.errors.map(asRecord) : []
    const firstError = errors[0]
    const failureSummary = typeof firstError?.Message === 'string'
      ? firstError.Message
      : typeof firstError?.message === 'string'
        ? firstError.message
        : ''
    const failureCountText = Number.isFinite(failedChecks) && failedChecks > 0
      ? `${failedChecks} failed check${failedChecks === 1 ? '' : 's'}`
      : 'a failed final validation'

    return {
      label: `Auto-validation repair ${validationAttempt}`,
      detail: `Triggered because the prior automatic final validation had ${failureCountText}.${failureSummary ? ` ${failureSummary}` : ''}`,
      className: 'border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300',
    }
  }

  if (itemID.includes('reflection')) {
    return {
      label: 'Message-sequence reflection',
      detail: plannedMessage
        ? 'This is the sequence’s closing reflection turn. The planned instruction is shown below.'
        : 'This is the sequence’s closing reflection turn, not a workflow retry or another orchestrator dispatch. Its exact sent prompt is available when you expand this entry.',
      className: 'border-border bg-muted text-muted-foreground',
      plannedMessage,
    }
  }

  const retryAttempt = Number(content?.retry_attempt)
  if (Number.isFinite(retryAttempt) && retryAttempt > 1) {
    return {
      label: `Runtime retry ${retryAttempt}`,
      detail: 'The runtime retried this same execution after a transient failure; it was not a new planned item or orchestrator call.',
      className: 'border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300',
    }
  }

  if (itemID) {
    return {
      label: plannedMessage ? 'Planned sequence item' : 'Recorded sequence item',
      detail: plannedMessage
        ? `The plan requested the agent item “${itemID}”.`
        : `The runtime recorded the agent item “${itemID}”. Its exact sent prompt is available when you expand this entry.`,
      className: 'border-border bg-muted text-muted-foreground',
      plannedMessage,
    }
  }

  return {
    label: 'Execution origin not recorded',
    detail: 'This historical log does not contain a agent item, validation trigger, or retry marker.',
    className: 'border-border bg-muted text-muted-foreground',
  }
}

type SentAgentMessage = {
  label: string
  message: string
}

// Historical runs may pre-date a agent entry in plan.json, but the
// durable conversation still preserves each human/planner message that was
// sent to the agent. Keep it separate from the system prompt and tool traffic.
export const getSentAgentMessages = (conversation: string): SentAgentMessage[] => {
  const data = asRecord(parseJsonLike(conversation))
  const history = Array.isArray(data?.conversation_history) ? data.conversation_history : []

  return history
    .map(asRecord)
    .filter(entry => entry?.Role === 'human' || entry?.role === 'human' || entry?.Role === 'user' || entry?.role === 'user')
    .map(entry => {
      const message = (Array.isArray(entry?.Parts) ? entry.Parts : Array.isArray(entry?.parts) ? entry.parts : [])
        .map(asRecord)
        .map(part => typeof part?.Text === 'string' ? part.Text : typeof part?.text === 'string' ? part.text : '')
        .filter(Boolean)
        .join('\n')
        .trim()
      if (!message) return null
      const label = message.includes('## Learnings Contribution')
        ? 'Planner learnings-contribution message'
        : message.includes('## Pre-Validation Failed')
          ? 'Automatic validation-repair message'
          : message.startsWith('**DESCRIPTION**:')
            ? 'Planner execution message'
            : 'Message sent to agent'
      return { label, message }
    })
    .filter((item): item is SentAgentMessage => item !== null)
}

export const getStepIcon = (type: string) => {
  switch (type) {
    case 'orchestration':
      return <Network className="w-4 h-4 text-purple-500" />
    case 'orchestrator':
    case 'todo_task':
      return <ListTodo className="w-4 h-4 text-purple-500" />
    case 'human_input':
      return <User className="w-4 h-4 text-orange-500" />
    case 'sub-agent':
      return <Bot className="w-4 h-4 text-indigo-500" />
    case 'agent':
      return <MessageSquare className="w-4 h-4 text-teal-500" />
    case 'regular':
      return <FileText className="w-4 h-4 text-muted-foreground" />
    default:
      return <FileText className="w-4 h-4 text-muted-foreground" />
  }
}

// Parse step ID into sortable segments
// step-1 → [1]
// step-8-sub-agent-2 → [8, 'sub-agent', 2]
// step-8-sub-agent-2-sub-agent-1 → [8, 'sub-agent', 2, 'sub-agent', 1]
const parseStepId = (stepId: string): (string | number)[] => {
  const segments: (string | number)[] = []

  // Remove 'step-' prefix and split by patterns
  const withoutPrefix = stepId.replace(/^step-/, '')

  // Match nested sub-agent and route identifiers.
  const pattern = /(\d+|sub-agent|sub|generic)/g
  let match
  while ((match = pattern.exec(withoutPrefix)) !== null) {
    const val = match[1]
    if (val === 'sub-agent' || val === 'sub' || val === 'generic') {
      segments.push(val)
    } else {
      segments.push(parseInt(val, 10))
    }
  }

  return segments
}

// Sort step IDs so nested items appear after their parent
const sortStepIds = (a: string, b: string): number => {
  const segA = parseStepId(a)
  const segB = parseStepId(b)

  const minLen = Math.min(segA.length, segB.length)

  for (let i = 0; i < minLen; i++) {
    const valA = segA[i]
    const valB = segB[i]

    // Both numbers - compare numerically
    if (typeof valA === 'number' && typeof valB === 'number') {
      if (valA !== valB) return valA - valB
    }
    // Both strings - compare alphabetically
    else if (typeof valA === 'string' && typeof valB === 'string') {
      if (valA !== valB) return valA.localeCompare(valB)
    }
    // Mixed - numbers come before strings
    else if (typeof valA === 'number') {
      return -1
    } else {
      return 1
    }
  }

  if (segA.length === 0 && segB.length === 0) {
    return a.localeCompare(b)
  }

  // Shorter one (parent) comes first
  return segA.length - segB.length
}

const timestampToMs = (value: unknown): number => {
  if (typeof value !== 'string' || value.trim() === '') return 0
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : 0
}

const firstPositive = (...values: number[]) => values.find(value => value > 0) || 0

const minPositive = (values: number[]) => {
  const positives = values.filter(value => value > 0)
  return positives.length > 0 ? Math.min(...positives) : 0
}

const getExecutionStartedAtMs = (exec: unknown): number => {
  const execRecord = asRecord(exec)
  const content = asRecord(execRecord?.content)
  const timing = asRecord(execRecord?.timing) || asRecord(content?.timing)
  const agent = asRecord(timing?.agent)

  return firstPositive(
    timestampToMs(agent?.started_at),
    timestampToMs(content?.started_at),
    timestampToMs(execRecord?.started_at),
    timestampToMs(content?.timestamp),
    timestampToMs(agent?.completed_at),
    timestampToMs(content?.completed_at),
  )
}

export const getStepFirstActivityMs = (stepLogs: unknown): number => {
  const stepRecord = asRecord(stepLogs)
  if (!stepRecord) return 0

  const timestamps: number[] = []
  const addTimestamp = (value: unknown) => {
    const ms = timestampToMs(value)
    if (ms > 0) timestamps.push(ms)
  }

  if (Array.isArray(stepRecord.executions)) {
    stepRecord.executions.forEach(exec => {
      const ms = getExecutionStartedAtMs(exec)
      if (ms > 0) timestamps.push(ms)
    })
  }

  ;['learnings', 'todo_task', 'orchestration', 'validations'].forEach(key => {
    const items = stepRecord[key]
    if (!Array.isArray(items)) return
    items.forEach(item => {
      const record = asRecord(item)
      addTimestamp(record?.timestamp)
      addTimestamp(asRecord(record?.content)?.timestamp)
    })
  })

  return minPositive(timestamps)
}

export const sortStepEntriesByExecution = (
  a: [string, unknown],
  b: [string, unknown],
): number => {
  const aStartedAt = getStepFirstActivityMs(a[1])
  const bStartedAt = getStepFirstActivityMs(b[1])

  if (aStartedAt > 0 && bStartedAt > 0 && aStartedAt !== bStartedAt) {
    return aStartedAt - bStartedAt
  }
  if (aStartedAt > 0 && bStartedAt === 0) return -1
  if (aStartedAt === 0 && bStartedAt > 0) return 1
  return sortStepIds(a[0], b[0])
}

// Calculate nesting level based on step ID pattern
export const getStepNestingLevel = (stepId: string): number => {
  const segments = parseStepId(stepId)
  let level = 0

  for (const seg of segments) {
    if (seg === 'sub-agent' || seg === 'sub' || seg === 'generic') {
      level++
    }
  }

  return level
}

// Determine the nesting context (what type of parent this is nested under)
const getStepNestingContext = (stepId: string): 'none' | 'sub-agent' => {
  const lastSubIndex = Math.max(stepId.lastIndexOf('-sub-'), stepId.lastIndexOf('-generic-'))
  const lastSubAgentIndex = Math.max(stepId.lastIndexOf('-sub-agent-'), lastSubIndex)

  return lastSubAgentIndex === -1 ? 'none' : 'sub-agent'
}

// Get the indentation style for a step based on its nesting level
export const getStepIndentStyle = (level: number): React.CSSProperties => {
  if (level === 0) return {}
  return { marginLeft: `${level * 32}px` }
}

// Get additional CSS class for nested steps (colored left border)
export const getStepNestingClass = (stepId: string): string => {
  const context = getStepNestingContext(stepId)

  switch (context) {
    case 'sub-agent':
      return 'border-l-4 border-l-purple-500/50'
    default:
      return ''
  }
}

type LogRecord = Record<string, unknown>

type StepMetrics = {
  inputTokens: number
  outputTokens: number
  totalTokens: number
  cacheTokens: number
  reasoningTokens: number
  durationMs: number
  llmCalls: number
}

export const asRecord = (value: unknown): LogRecord | null => (
  value && typeof value === 'object' && !Array.isArray(value) ? value as LogRecord : null
)

export const asNumber = (value: unknown): number => {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : 0
  }
  return 0
}

const durationFromTimestamps = (start: unknown, end: unknown): number => {
  if (typeof start !== 'string' || typeof end !== 'string') return 0
  const startMs = Date.parse(start)
  const endMs = Date.parse(end)
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || endMs < startMs) return 0
  return endMs - startMs
}

export const formatTokenCount = (tokens: number): string => {
  if (!tokens) return '0'
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens >= 10_000_000 ? 1 : 2)}M`
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(tokens >= 100_000 ? 0 : 1)}k`
  return `${tokens}`
}

export const formatDuration = (durationMs: number): string => {
  if (!durationMs) return '0s'
  const totalSeconds = Math.max(1, Math.round(durationMs / 1000))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60

  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}

// Built once: this is called per step card on every 2.5 s poll render.
const stepStartedAtFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
})

export const formatStepStartedAt = (timestampMs: number): string => stepStartedAtFormatter.format(new Date(timestampMs))

const addCallTokens = (metrics: StepMetrics, call: LogRecord) => {
  metrics.inputTokens += asNumber(call.prompt_tokens)
  metrics.outputTokens += asNumber(call.completion_tokens)
  metrics.cacheTokens += asNumber(call.cache_tokens)
  metrics.reasoningTokens += asNumber(call.reasoning_tokens)

  const totalTokens = asNumber(call.total_tokens)
  if (totalTokens > 0) {
    metrics.totalTokens += totalTokens
  } else {
    metrics.totalTokens += asNumber(call.prompt_tokens) + asNumber(call.completion_tokens) + asNumber(call.reasoning_tokens)
  }
}

export const getExecutionMetrics = (exec: unknown): StepMetrics => {
  const metrics: StepMetrics = {
    inputTokens: 0,
    outputTokens: 0,
    totalTokens: 0,
    cacheTokens: 0,
    reasoningTokens: 0,
    durationMs: 0,
    llmCalls: 0,
  }

  const execRecord = asRecord(exec)
  const content = asRecord(execRecord?.content)
  const timing = asRecord(execRecord?.timing) || asRecord(content?.timing) || content
  const agent = asRecord(timing?.agent) || content
  const llm = asRecord(timing?.llm)

  metrics.durationMs = asNumber(agent?.duration_ms) ||
    durationFromTimestamps(agent?.started_at, agent?.completed_at) ||
    asNumber(content?.duration_ms)
  metrics.llmCalls = asNumber(agent?.llm_call_count) || asNumber(llm?.count)

  const calls = Array.isArray(llm?.calls) ? llm.calls : []
  calls.forEach(call => {
    const callRecord = asRecord(call)
    if (callRecord) addCallTokens(metrics, callRecord)
  })

  if (calls.length === 0 && content) {
    addCallTokens(metrics, content)
  }

  return metrics
}

export const getStepMetrics = (executions: unknown[]): StepMetrics => executions.reduce<StepMetrics>((acc, exec) => {
  const metrics = getExecutionMetrics(exec)
  acc.inputTokens += metrics.inputTokens
  acc.outputTokens += metrics.outputTokens
  acc.totalTokens += metrics.totalTokens
  acc.cacheTokens += metrics.cacheTokens
  acc.reasoningTokens += metrics.reasoningTokens
  acc.durationMs += metrics.durationMs
  acc.llmCalls += metrics.llmCalls
  return acc
}, {
  inputTokens: 0,
  outputTokens: 0,
  totalTokens: 0,
  cacheTokens: 0,
  reasoningTokens: 0,
  durationMs: 0,
  llmCalls: 0,
})

// Most recent execution attempt's model — fast-path/scripted attempts carry
// no model field, so those are skipped in favor of the latest LLM attempt.
// Picked by actual completed_at/started_at timestamp, NOT array position or
// "attempt N" number: attempt slots are fixed retry-slot labels, not
// chronological order — a fresh top-level re-run overwrites the "attempt 1"
// slot while an older "attempt 2" from a completely different run can sit
// right next to it, many hours apart. Verified live: a step's array had
// attempt-1 newest (just re-run) and attempt-2 from the prior day.
export const getStepModel = (executions: unknown[]): string | null => {
  let bestModel: string | null = null
  let bestTimeMs = -Infinity
  for (const exec of executions) {
    const execRecord = asRecord(exec)
    if (execRecord?.fast_path === true) continue
    const content = asRecord(execRecord?.content)
    const model = content?.model
    if (typeof model !== 'string' || !model.trim()) continue
    const timestamp = content?.completed_at ?? content?.started_at
    const timeMs = typeof timestamp === 'string' ? Date.parse(timestamp) : NaN
    if (Number.isNaN(timeMs)) {
      // No timestamp to compare — only use it if nothing better has been found yet.
      if (bestModel === null) bestModel = model
      continue
    }
    if (timeMs > bestTimeMs) {
      bestTimeMs = timeMs
      bestModel = model
    }
  }
  return bestModel
}

export const hasStepMetrics = (metrics: StepMetrics) => (
  metrics.durationMs > 0 || metrics.totalTokens > 0 || metrics.inputTokens > 0 || metrics.outputTokens > 0 || metrics.llmCalls > 0
)

export const hasLearningSignal = (stepLogs: {
  learnings?: unknown[]
  learning_objective?: string
  learnings_access?: string
}) => (
  (stepLogs.learnings?.length || 0) > 0 ||
  Boolean(stepLogs.learning_objective?.trim()) ||
  Boolean(stepLogs.learnings_access && stepLogs.learnings_access !== 'none')
)

export const hasKnowledgebaseSignal = (stepLogs: {
  knowledgebase_access?: string
  knowledgebase_contribution?: string
}) => (
  Boolean(stepLogs.knowledgebase_contribution?.trim()) ||
  Boolean(stepLogs.knowledgebase_access && stepLogs.knowledgebase_access !== 'none')
)

export const getAgentReflection = (stepLogs: StepExecutionLogs) => {
  const entries = stepLogs.agent?.entries || []
  return entries.find(entry => entry.item_id === `${stepLogs.step_id}-reflection`) || null
}

export const getStepTypeLabel = (type: string): string => {
  switch (type) {
    case 'orchestration':
      return 'Orchestration'
    case 'routing':
      return 'Routing'
    case 'branch':
      return 'Branch'
    case 'orchestrator':
    case 'todo_task':
      return 'Agent'
    case 'human_input':
      return 'Human Input'
    case 'sub-agent':
      return 'Sub-Agent'
    case 'agent':
      return 'Agent'
    case 'crew':
      return 'Crew'
    case 'regular':
    default:
      return 'Scripted'
  }
}

export const getStepTypeDescription = (type: string): string => {
  switch (type) {
    case 'orchestrator':
    case 'todo_task':
      return 'Orchestrator: decides which delegated tasks to run and tracks their outcomes.'
    case 'sub-agent':
      return 'Sub-agent: a child task dispatched by an agent.'
    case 'agent':
      return 'Agent: completes an ordered series of instructions and conversation turns.'
    case 'routing':
      return 'Routing step: a major, self-contained sub-workflow fork that deterministically selects the next path.'
    case 'branch':
      return 'Branch step: a small in-flow decision that deterministically selects the next step.'
    case 'human_input':
      return 'Human-input step: waits for an operator response before continuing.'
    case 'crew':
      return 'Crew step: invokes a persistent crew trigger and waits for its final response.'
    case 'regular':
    default:
      return 'Scripted step: runs deterministic Python code for this step.'
  }
}

export const getStepTypeBadgeStyle = (type: string): string => {
  switch (type) {
    case 'orchestration':
      return 'bg-purple-500/10 text-purple-600 border-purple-500/20 dark:bg-purple-500/20 dark:text-purple-300'
    case 'routing':
      return 'bg-indigo-500/10 text-indigo-600 border-indigo-500/20 dark:bg-indigo-500/20 dark:text-indigo-300'
    case 'branch':
      return 'bg-cyan-500/10 text-cyan-600 border-cyan-500/20 dark:bg-cyan-500/20 dark:text-cyan-300'
    case 'orchestrator':
    case 'todo_task':
      return 'bg-fuchsia-500/10 text-fuchsia-600 border-fuchsia-500/20 dark:bg-fuchsia-500/20 dark:text-fuchsia-300'
    case 'human_input':
      return 'bg-orange-500/10 text-orange-600 border-orange-500/20 dark:bg-orange-500/20 dark:text-orange-300'
    case 'sub-agent':
      return 'bg-blue-500/10 text-blue-600 border-blue-500/20 dark:bg-blue-500/20 dark:text-blue-300'
    case 'agent':
      return 'bg-teal-500/10 text-teal-600 border-teal-500/20 dark:bg-teal-500/20 dark:text-teal-300'
    case 'crew':
      return 'bg-sky-500/10 text-sky-600 border-sky-500/20 dark:bg-sky-500/20 dark:text-sky-300'
    case 'regular':
    default:
      return 'bg-slate-500/10 text-slate-600 border-slate-500/20 dark:bg-slate-500/20 dark:text-slate-300'
  }
}

// Helper to determine the overall real-time status of a step
export const getStepStatus = (stepLogs: StepExecutionLogs): 'completed' | 'failed' | 'running' | 'pending' => {
  const validations = stepLogs.validations || []
  const executions = stepLogs.executions || []
  const orchestration = stepLogs.orchestration || []
  const todoTask = stepLogs.todo_task || []

  if (stepLogs.type === 'agent') {
    if (stepLogs.agent_status === 'failed') return 'failed'
    if (stepLogs.agent_status === 'completed') return 'completed'
    if (stepLogs.agent_status === 'running') return 'running'
    return 'pending'
  }

  // A saved Python script can fail before a validation record is written.
  // Its last fast-path execution carries the terminal failure directly. Other
  // step types may recover from an earlier failed attempt, so do not treat
  // any historical execution error as their final status.
  const latestExecution = executions[executions.length - 1]
  if ((latestExecution?.fast_path || latestExecution?.content?.mode === 'scripted_fast_path') &&
    (latestExecution.content?.success === false ||
      (typeof latestExecution.content?.exit_code === 'number' && latestExecution.content.exit_code !== 0))) {
    return 'failed'
  }

  // Check validations first for finality
  if (validations.length > 0) {
    if (validations.some(v => v.content?.execution_status === 'FAILED')) {
      return 'failed'
    }
    const latestVal = validations[validations.length - 1]
    if (latestVal.content?.execution_status === 'COMPLETED') {
      return 'completed'
    }
    if (latestVal.content?.execution_status === 'RUNNING' || latestVal.content?.execution_status === 'PENDING') {
      return 'running'
    }
  }

  // If executions exist but validations aren't finalized or present:
  if (executions.length > 0) {
    // If the step has no success criteria, execution completion means the step is completed.
    if (!stepLogs.success_criteria) {
      return 'completed'
    }
    return 'running'
  }

  // Orchestration and todo-task routing records are emitted after their work completes.
  if (orchestration.length > 0 || todoTask.length > 0) {
    return 'completed'
  }

  return 'pending'
}

/** Display title with the same fallback chain the step list renders. */
export const getStepDisplayTitle = (stepId: string, stepLogs: StepExecutionLogs): string => {
  const title = stepLogs.title?.trim()
  return title ? title : (stepLogs.original_id || stepId)
}

/** Latest human-readable failure across executions and validations, newest first. */
export const getStepLatestError = (stepLogs: StepExecutionLogs): string => {
  const executions = stepLogs.executions || []
  for (let index = executions.length - 1; index >= 0; index -= 1) {
    const content = (executions[index] as { content?: { error?: unknown } })?.content
    if (typeof content?.error === 'string' && content.error.trim()) {
      return content.error.replace(/\s+/g, ' ').trim()
    }
  }
  const validations = stepLogs.validations || []
  for (let index = validations.length - 1; index >= 0; index -= 1) {
    const errors = (validations[index] as { content?: { errors?: unknown } })?.content?.errors
    const first = Array.isArray(errors) ? errors[0] as { Message?: unknown; message?: unknown } : null
    const text = typeof first?.Message === 'string' ? first.Message : typeof first?.message === 'string' ? first.message : ''
    if (text.trim()) return text.replace(/\s+/g, ' ').trim()
  }
  return ''
}

/** Friendly run picker label: "iteration-84-sched/default" reads as "Run 84 · sched · default". */
export const formatRunFolderLabel = (folder: string): string => {
  const match = /^iteration-(\d+)(.*)$/.exec(folder.trim())
  if (!match) return folder
  const rest = match[2]
    .replace(/^-hook\b/, 'Webhook')
    .replace(/^[/-]/, '')
    .split('/')
    .map(part => part.trim())
    .filter(Boolean)
  return rest.length > 0 ? `Run ${match[1]} · ${rest.join(' · ')}` : `Run ${match[1]}`
}
