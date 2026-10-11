import type { TerminalSnapshot } from '../services/api-types'

export type TerminalRailSection = 'active' | 'attention' | 'workflow' | 'review' | 'other'
export type TerminalRailVisualKind =
  | 'terminal'
  | 'orchestrator'
  | 'sub-agent'
  | 'agent'
  | 'routing'
  | 'scripted'
  | 'evaluation'
  | 'reviewer'

export interface TerminalRailLogicalGroup {
  key: string
  title: string
  section: TerminalRailSection
  representative: TerminalSnapshot
  // One representative per real attempt. Lifecycle and raw-turn companion
  // records are retained in members for selection/search, but are not attempts.
  terminals: TerminalSnapshot[]
  members: TerminalSnapshot[]
}

interface OrganizeTerminalRailOptions {
  getState: (terminal: TerminalSnapshot) => string
  isMainAgent: (terminal: TerminalSnapshot) => boolean
}

const REVIEW_LABEL_PATTERN = /\b(review(?:er)?|critic|advisor|audit|pulse|health|harden|maintenance)\b/i
const AGENT_PATTERN = /^message[-_ ]sequence(?:[-_ ].*)?$/i

function normalizedReviewIdentity(values: Array<string | undefined>): string {
  return values.filter(Boolean).join(' ')
    .replace(/[-_:]+/g, ' ')
    .replace(/\s+/g, ' ')
    .toLowerCase()
}

function pulseReviewTitleFromIdentity(searchable: string): string {
  if (!searchable.includes('pulse review')) return ''

  const candidates: Array<{ title: string; phrases: string[] }> = [
    { title: 'Bug review', phrases: ['bug review'] },
    { title: 'Evaluation health', phrases: ['eval health', 'evaluation health'] },
    { title: 'Learning health', phrases: ['learning health'] },
    { title: 'Knowledge base review', phrases: ['knowledge base'] },
    { title: 'Database health', phrases: ['database health', 'db health'] },
    { title: 'Engineering review', phrases: ['report health', 'evaluation health', 'eval health'] },
    { title: 'Artifact review', phrases: ['artifact review'] },
    { title: 'Ops review', phrases: ['llm ops', 'ops review'] },
    { title: 'Goal Advisor', phrases: ['goal advisor'] },
  ]
  const matches = candidates.flatMap(candidate => candidate.phrases
    .map(phrase => ({ title: candidate.title, index: searchable.indexOf(phrase) }))
    .filter(match => match.index >= 0))
    .sort((a, b) => a.index - b.index)
  if (matches.length > 0) return matches[0].title
  return 'Pulse review'
}

function pulseReviewTitle(terminal: TerminalSnapshot): string {
  // Nested reviewers can carry both their parent's module name and an
  // incorrectly inherited child label. Runtime ownership is authoritative:
  // the first module in the parent/terminal identity is the review the user
  // launched. Human-facing labels are only a fallback for older snapshots.
  const ownershipTitle = pulseReviewTitleFromIdentity(normalizedReviewIdentity([
    terminal.parent_step_id,
    terminal.owner_id,
    terminal.terminal_id,
  ]))
  if (ownershipTitle && ownershipTitle !== 'Pulse review') return ownershipTitle
  const descriptiveTitle = pulseReviewTitleFromIdentity(normalizedReviewIdentity([
    terminal.agent_name,
    terminal.step_name,
    terminal.display_title,
    terminal.step_id,
    terminal.execution_id,
  ]))
  return descriptiveTitle || ownershipTitle
}

function humanize(value?: string): string {
  if (!value) return ''
  const cleaned = value
    .replace(/^todo[-_:]sub[-_:]step[-_:]\d+[-_:]sub[-_:]/i, '')
    .replace(/^step[-_:]\d+[-_:](?:sub|execution)[-_:]/i, '')
    .replace(/^exec[-_:]/i, '')
    .replace(/^main[-_:]/i, '')
    .replace(/[-_]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  if (!cleaned) return ''
  return cleaned.charAt(0).toUpperCase() + cleaned.slice(1)
}

export function terminalRailTitle(terminal: TerminalSnapshot): string {
  const reviewTitle = pulseReviewTitle(terminal)
  if (reviewTitle) return reviewTitle
  // Runtime display_title often includes the workflow breadcrumb
  // ("linkedin -> ..."). Prefer the intrinsic agent name so the compact rail
  // shows the task itself rather than repeating the selected workflow.
  const preferred = terminal.step_name || terminal.agent_name || terminal.display_title || terminal.step_id || terminal.label
  if (preferred && AGENT_PATTERN.test(preferred) && terminal.parent_step_id) {
    return `${humanize(terminal.parent_step_id)} sequence`
  }
  return humanize(preferred) || 'Agent'
}

export function terminalRailVisualKind(terminal: TerminalSnapshot): TerminalRailVisualKind {
  const executionKind = (terminal.execution_kind || terminal.scope || '').trim().toLowerCase()
  const stepType = (terminal.step_type || '').trim().toLowerCase()
  const executionMode = (terminal.step_execution_mode || '').trim().toLowerCase()
  const agentName = (terminal.agent_name || '').trim()
  const displayName = `${terminal.agent_name || ''} ${terminal.display_title || ''}`.trim()
  const stepID = (terminal.step_id || '').trim().toLowerCase()
  const parentStepID = (terminal.parent_step_id || '').trim().toLowerCase()
  // Every top-level workflow terminal is rooted under the synthetic main
  // agent ID. That is navigation metadata, not evidence that the step is a
  // delegated sub-agent. Only a real plan-step parent changes the visual role.
  const hasDistinctParentStep = Boolean(
    parentStepID &&
    parentStepID !== stepID &&
    !parentStepID.startsWith('main_agent:'),
  )

  const reviewTitle = pulseReviewTitle(terminal)
  if (reviewTitle === 'Evaluation health') return 'evaluation'
  if (reviewTitle) return 'reviewer'
  if (
    /^eval(?:uation)?(?:[-_:]|$)/i.test(stepID) ||
    /\bevaluation\b/i.test(displayName)
  ) return 'evaluation'
  if (executionMode === 'scripted' || executionKind === 'scripted_step') return 'scripted'
  if (stepType === 'routing' || stepType === 'router' || executionKind === 'router') return 'routing'
  if (
    /\bfull workflow execution\b/i.test(displayName) ||
    (!stepID && /(?:^|:)workflow-full-[^:]+$/i.test(terminal.terminal_id))
  ) return 'orchestrator'
  if (
    stepType === 'todo_task' ||
    stepType === 'orchestrator' ||
    executionKind === 'orchestrator' ||
    executionKind === 'todo_task'
  ) return hasDistinctParentStep ? 'sub-agent' : 'agent'
  // A predefined route can use agent internally, but its
  // user-facing role is still a child agent of the owning agent.
  if (hasDistinctParentStep) return 'sub-agent'
  if (
    (stepType === 'agent' || stepType === 'agent') ||
    (stepType === 'regular' && executionMode !== 'scripted') ||
    (executionKind === 'agent' || executionKind === 'agent') ||
    (executionKind === 'agent_item' || executionKind === 'agent_item') ||
    AGENT_PATTERN.test(agentName)
  ) return 'agent'
  if ([
    'sub_agent',
    'subagent',
    'delegation',
    'background_agent',
    'background',
    'workflow_sub_agent',
    'workflow_generic_agent',
    'generic_agent',
    'pulse_reviewer',
    'workshop_background',
  ].includes(executionKind)) return 'sub-agent'
  return 'terminal'
}

export function terminalRailLogicalKey(terminal: TerminalSnapshot): string {
  const title = terminalRailTitle(terminal).toLowerCase()
  const rawName = (terminal.agent_name || terminal.step_name || '').trim()
  const reviewTitle = pulseReviewTitle(terminal)

  // Retries of the same Pulse reviewer are one user-facing agent with attempt
  // history, even though each retry receives a fresh runtime execution ID.
  if (reviewTitle) return `review:${reviewTitle.toLowerCase()}`

  // A agent creates one terminal per turn. Keep those turns under
  // the owning plan step instead of presenting each turn as a separate agent.
  if ((terminal.step_type || '').toLowerCase() === 'agent' || AGENT_PATTERN.test(rawName)) {
    const stepID = (terminal.step_id || '').trim().toLowerCase()
    // Current metadata uses step_id for the owning sequence (for example
    // calc-task and word-task) and parent_step_id for their shared manager.
    // Older turn records used agent-<item> as step_id, in which
    // case parent_step_id remains the only stable owning identity.
    const owningStep = stepID && !AGENT_PATTERN.test(stepID)
      ? stepID
      : (terminal.parent_step_id || '').trim().toLowerCase() || stepID
    return `step:${owningStep || title}`
  }

  if (terminal.step_id) {
    // Plan step IDs are workflow-wide identities. Runtime parent IDs vary
    // between direct runs and orchestrator runs, so including the parent here
    // creates duplicate rows for the same logical step.
    return `step:${terminal.step_id.trim().toLowerCase()}`
  }

  const kind = (terminal.execution_kind || terminal.scope || 'agent').trim().toLowerCase()
  const parent = (terminal.parent_step_id || '').trim().toLowerCase()
  if (title !== 'agent') {
    return `${kind}:${parent}:${title}`
  }
  return `terminal:${terminal.terminal_id}`
}

export function terminalRailGroupSearchText(group: TerminalRailLogicalGroup): string {
  return group.members.map(terminal => [
    group.title,
    terminal.step_name,
    terminal.display_title,
    terminal.agent_name,
    terminal.step_id,
    terminal.parent_step_id,
    terminal.execution_kind,
    terminal.status?.provider_label,
    terminal.status?.status_text,
  ].filter(Boolean).join(' ')).join(' ').toLowerCase()
}

function isReviewTerminal(terminal: TerminalSnapshot, title: string): boolean {
  const owner = `${terminal.owner_id || ''} ${terminal.execution_id || ''}`
  const searchableLabel = `${title} ${terminal.agent_name || ''} ${terminal.step_name || ''} ${owner}`
    .replace(/[-_:]+/g, ' ')
  return REVIEW_LABEL_PATTERN.test(searchableLabel)
}

function isWorkflowTerminal(terminal: TerminalSnapshot): boolean {
  const kind = (terminal.execution_kind || terminal.scope || '').toLowerCase()
  return Boolean(terminal.step_id || terminal.parent_step_id) || [
    'workflow_step',
    'execution_only',
    'step',
    'todo_task',
    'orchestrator',
    'sub_agent',
    'agent',
    'agent_item',
    'scripted_step',
    'router',
    'delegation',
  ].includes(kind)
}

function isFullRunContainerTerminal(terminal: TerminalSnapshot): boolean {
  const kind = (terminal.execution_kind || terminal.scope || '').trim().toLowerCase()
  if (kind === 'full_run') return true
  const identity = `${terminal.execution_id || ''} ${terminal.owner_id || ''} ${terminal.terminal_id}`
  return /(?:^|[:\s])workflow-full-[^:\s]+/i.test(identity) &&
    /\bfull workflow execution\b/i.test(`${terminal.agent_name || ''} ${terminal.display_title || ''}`)
}

function updatedTime(terminal: TerminalSnapshot): number {
  return new Date(terminal.updated_at || terminal.created_at || '').getTime() || 0
}

function createdTime(terminal: TerminalSnapshot): number {
  return new Date(terminal.created_at || terminal.updated_at || '').getTime() || 0
}

function representativeFor(
  terminals: TerminalSnapshot[],
  getState: OrganizeTerminalRailOptions['getState'],
): TerminalSnapshot {
  const running = terminals.filter(terminal => getState(terminal) === 'running')
  const candidates = running.length > 0 ? running : terminals
  return [...candidates].sort((a, b) => {
    const attemptDelta = (b.step_attempt || 0) - (a.step_attempt || 0)
    if (attemptDelta !== 0) return attemptDelta
    return updatedTime(b) - updatedTime(a)
  })[0]
}

function delegationIdentity(terminal: TerminalSnapshot): string {
  return [
    terminal.terminal_id,
    terminal.execution_id,
    terminal.owner_id,
  ].filter(Boolean).join(' ').toLowerCase()
}

function isDelegationLifecycleWrapper(terminal: TerminalSnapshot): boolean {
  return terminal.terminal_id.includes(':todo-sub-') && !terminal.terminal_id.includes(':workflow-step:')
}

function isPulseReviewLifecycleWrapper(terminal: TerminalSnapshot): boolean {
  return Boolean(pulseReviewTitle(terminal)) && !terminal.terminal_id.includes(':workflow-step:')
}

function runtimeAgentName(terminal: TerminalSnapshot): string {
  const value = terminal.agent_name || terminal.display_title || ''
  return value
    .replace(/^.*?\s*->\s*/i, '')
    .replace(/\s+/g, ' ')
    .trim()
    .toLowerCase()
}

function workflowStepParentExecution(terminal: TerminalSnapshot): string {
  const sessionPrefix = `${terminal.session_id}:`
  const owner = (terminal.owner_id || (
    terminal.terminal_id.startsWith(sessionPrefix)
      ? terminal.terminal_id.slice(sessionPrefix.length)
      : ''
  )).trim()
  if (!owner.startsWith('workflow-step:')) return ''

  const stepID = (terminal.step_id || '').trim()
  const suffix = stepID ? `:${stepID}` : ''
  if (!suffix || !owner.endsWith(suffix)) return ''
  return owner.slice('workflow-step:'.length, -suffix.length).trim()
}

function isStandaloneLifecycleRoot(terminal: TerminalSnapshot): boolean {
  if (terminal.step_id || terminal.terminal_id.includes(':workflow-step:')) return false
  const kind = (terminal.execution_kind || terminal.scope || '').trim().toLowerCase()
  return [
    'background_agent',
    'background',
    'sub_agent',
    'workflow_sub_agent',
    'workshop_background',
    'execution',
  ].includes(kind)
}

// A predefined route produces two runtime records:
//   1. todo-sub-<step-id>-...: lifecycle/notification wrapper
//   2. workflow-step:...:<step-id>: the real agent transcript
// They are one user-facing agent. Resolve the wrapper onto the real step's
// logical key so the rail does not present both as separate terminals.
function runtimeAliases(terminals: TerminalSnapshot[]): Map<string, string> {
  const aliases = new Map<string, string>()
  const realStepsBySession = new Map<string, TerminalSnapshot[]>()
  const executionRoots = new Map<string, TerminalSnapshot>()

  for (const terminal of terminals) {
    if (
      terminal.execution_id &&
      terminal.terminal_id === `${terminal.session_id}:${terminal.execution_id}`
    ) {
      executionRoots.set(`${terminal.session_id}:${terminal.execution_id}`, terminal)
    }
    if (
      !terminal.step_id ||
      isDelegationLifecycleWrapper(terminal) ||
      !terminal.terminal_id.includes(':workflow-step:')
    ) {
      continue
    }
    const candidates = realStepsBySession.get(terminal.session_id) || []
    candidates.push(terminal)
    realStepsBySession.set(terminal.session_id, candidates)
  }

  // Structured/background execution also retains a raw CLI turn record:
  //   <session>:<execution>
  //   <session>:<execution>:turn-<id>
  // The latter is useful as fallback content, but it is not another agent or
  // another retry. Attach it to the clean execution root and hide it from the
  // attempt count.
  for (const terminal of terminals) {
    if (!terminal.execution_id || !terminal.terminal_id.includes(':turn-')) continue
    const root = executionRoots.get(`${terminal.session_id}:${terminal.execution_id}`)
    if (root) aliases.set(terminal.terminal_id, terminalRailLogicalKey(root))
  }

  // A standalone background tool emits a lifecycle root and a richer
  // workflow-step transcript beneath the same execution. They are one agent,
  // even when their labels differ (for example "Review Workflow Plan" versus
  // "Review plan"). Match by the declared execution ancestry rather than
  // fuzzy title text. Only collapse roots with exactly one concrete child;
  // a real orchestrator with several children remains independently visible.
  const childrenByRoot = new Map<string, TerminalSnapshot[]>()
  for (const child of terminals) {
    if (!child.step_id || !child.terminal_id.includes(':workflow-step:')) continue
    const parentExecution = workflowStepParentExecution(child)
    if (!parentExecution) continue
    const rootKey = `${child.session_id}:${parentExecution}`
    const root = executionRoots.get(rootKey)
    if (!root || !isStandaloneLifecycleRoot(root)) continue
    const children = childrenByRoot.get(rootKey) || []
    children.push(child)
    childrenByRoot.set(rootKey, children)
  }
  for (const [rootKey, children] of childrenByRoot) {
    if (children.length !== 1) continue
    const root = executionRoots.get(rootKey)
    if (root) aliases.set(root.terminal_id, terminalRailLogicalKey(children[0]))
  }

  for (const terminal of terminals) {
    if (!isDelegationLifecycleWrapper(terminal)) continue
    const identity = delegationIdentity(terminal)
    const candidates = realStepsBySession.get(terminal.session_id) || []
    // Prefer the most specific ID when one ID contains another.
    const target = [...candidates]
      .sort((a, b) => (b.step_id?.length || 0) - (a.step_id?.length || 0))
      .find(candidate => identity.includes(`todo-sub-${candidate.step_id?.toLowerCase()}-`))
    if (target) {
      aliases.set(terminal.terminal_id, terminalRailLogicalKey(target))
    }
  }

  // Evaluation execution emits both a real workflow-step transcript and a
  // step-less lifecycle record. Their agent names are identical, but only the
  // real transcript contains the events a user can inspect. Fold the wrapper
  // into the real step when the match is exact and unambiguous.
  for (const terminal of terminals) {
    if (
      terminal.step_id ||
      terminal.terminal_id.includes(':workflow-step:') ||
      aliases.has(terminal.terminal_id)
    ) {
      continue
    }
    const runtimeName = runtimeAgentName(terminal)
    if (!runtimeName) continue
    const candidates = (realStepsBySession.get(terminal.session_id) || [])
      .filter(candidate => runtimeAgentName(candidate) === runtimeName)
    if (candidates.length === 1) {
      aliases.set(terminal.terminal_id, terminalRailLogicalKey(candidates[0]))
    }
  }

  return aliases
}

function sectionFor(
  terminal: TerminalSnapshot,
  title: string,
  getState: OrganizeTerminalRailOptions['getState'],
): TerminalRailSection {
  const state = getState(terminal)
  if (state === 'running') return 'active'
  if (state === 'failed' || state === 'stale') return 'attention'
  if (isReviewTerminal(terminal, title)) return 'review'
  if (isWorkflowTerminal(terminal)) return 'workflow'
  return 'other'
}

function compareGroups(a: TerminalRailLogicalGroup, b: TerminalRailLogicalGroup): number {
  const aIndex = a.representative.step_index
  const bIndex = b.representative.step_index
  if (aIndex !== undefined && bIndex !== undefined && aIndex !== bIndex) return aIndex - bIndex
  if (aIndex !== undefined && bIndex === undefined) return -1
  if (aIndex === undefined && bIndex !== undefined) return 1
  const createdDelta = createdTime(a.representative) - createdTime(b.representative)
  if (createdDelta !== 0) return createdDelta
  return a.title.localeCompare(b.title)
}

export function organizeTerminalRail(
  terminals: TerminalSnapshot[],
  options: OrganizeTerminalRailOptions,
): TerminalRailLogicalGroup[] {
  const grouped = new Map<string, TerminalSnapshot[]>()
  const aliases = runtimeAliases(terminals)
  for (const terminal of terminals) {
    if (options.isMainAgent(terminal)) continue
    // A full workflow is an orchestration container. It has no conversation
    // of its own; the real, inspectable work is in its step terminals.
    // Keep a legacy shape check because old completion events lost full_run.
    if (isFullRunContainerTerminal(terminal)) continue
    const key = aliases.get(terminal.terminal_id) || terminalRailLogicalKey(terminal)
    const bucket = grouped.get(key) || []
    bucket.push(terminal)
    grouped.set(key, bucket)
  }

  return Array.from(grouped.entries()).map(([key, groupTerminals]) => {
    const sortedTerminals = [...groupTerminals].sort((a, b) => {
      const attemptDelta = (b.step_attempt || 0) - (a.step_attempt || 0)
      if (attemptDelta !== 0) return attemptDelta
      return updatedTime(b) - updatedTime(a)
    })
    const hasPulseReviewTranscript = sortedTerminals.some(terminal => (
      Boolean(pulseReviewTitle(terminal)) &&
      terminal.terminal_id.includes(':workflow-step:')
    ))
    const transcriptTerminals = sortedTerminals.filter(terminal => (
      !isDelegationLifecycleWrapper(terminal) &&
      !aliases.has(terminal.terminal_id) &&
      !(hasPulseReviewTranscript && isPulseReviewLifecycleWrapper(terminal))
    ))
    const attemptTerminals = transcriptTerminals.length > 0 ? transcriptTerminals : sortedTerminals
    const representative = representativeFor(
      attemptTerminals,
      options.getState,
    )
    const title = terminalRailTitle(representative)
    // Once the real transcript exists, its state is authoritative. A lifecycle
    // wrapper can settle a few seconds late (or be retained with stale
    // "running" metadata) and must not keep a completed agent spinning.
    const stateTerminals = attemptTerminals
    const hasRunningTerminal = stateTerminals.some(terminal => options.getState(terminal) === 'running')
    const hasFailedTerminal = stateTerminals.some(terminal => {
      const state = options.getState(terminal)
      return state === 'failed' || state === 'stale'
    })
    return {
      key,
      title,
      section: hasRunningTerminal
        ? 'active'
        : hasFailedTerminal
          ? 'attention'
          : sectionFor(representative, title, options.getState),
      representative,
      terminals: attemptTerminals,
      members: sortedTerminals,
    }
  }).sort(compareGroups)
}

// The rail defaults to active-only, but the pane can remain on a child after
// that child completes. Identify that one hidden group so the caller can keep
// it in its normal section until the user selects another terminal.
export function hiddenSelectedTerminalRailGroup(
  groups: TerminalRailLogicalGroup[],
  visibleGroups: TerminalRailLogicalGroup[],
  selectedTerminal?: TerminalSnapshot | null,
): TerminalRailLogicalGroup | null {
  if (!selectedTerminal) return null
  const selectedGroup = groups.find(group => group.members.some(terminal => (
    terminal.terminal_id === selectedTerminal.terminal_id &&
    (terminal.tmux_session || '') === (selectedTerminal.tmux_session || '')
  )))
  if (!selectedGroup || visibleGroups.some(group => group.key === selectedGroup.key)) return null
  return selectedGroup
}

// Lifecycle wrappers remain group members for search/status reconciliation,
// but they are not selectable transcripts. If one was selected before the
// richer child arrived, move the pane to the group's real representative.
export function canonicalTerminalRailSelection(
  groups: TerminalRailLogicalGroup[],
  selectedTerminal?: TerminalSnapshot | null,
): TerminalSnapshot | null {
  if (!selectedTerminal) return null
  const selectedGroup = groups.find(group => group.members.some(terminal => (
    terminal.terminal_id === selectedTerminal.terminal_id &&
    (terminal.tmux_session || '') === (selectedTerminal.tmux_session || '')
  )))
  if (!selectedGroup) return selectedTerminal
  const isSelectableTranscript = selectedGroup.terminals.some(terminal => (
    terminal.terminal_id === selectedTerminal.terminal_id &&
    (terminal.tmux_session || '') === (selectedTerminal.tmux_session || '')
  ))
  return isSelectableTranscript ? selectedTerminal : selectedGroup.representative
}
