import type { ActiveSessionInfo, TerminalSnapshot } from '../services/api-types'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import { isMainAgentTerminal } from './terminalIdentity'

type WorkflowPreset = CustomPreset | PredefinedPreset

function normalizeTerminalWorkflowPath(path?: string | null): string {
  return (path || '')
    .replace(/^\/data\/docs\//, '')
    .replace(/\/+$/, '')
}

function normalizeWorkflowIdentity(value?: string | null): string {
  return (value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function workflowPathParts(path?: string | null): string[] {
  return normalizeTerminalWorkflowPath(path).replace(/\\/g, '/').split('/').filter(Boolean)
}

function workflowNameFromPath(path?: string | null): string {
  const parts = workflowPathParts(path)
  const wfIdx = parts.findIndex(part => part.toLowerCase() === 'workflow')
  return wfIdx >= 0 && parts[wfIdx + 1] ? parts[wfIdx + 1] : (parts[parts.length - 1] || '')
}

export function workflowMatchKey(path?: string | null): string {
  return normalizeWorkflowIdentity(workflowNameFromPath(path))
}

function presetWorkflowKey(preset: WorkflowPreset): string {
  return workflowMatchKey(preset.selectedFolder?.filepath) || normalizeWorkflowIdentity(preset.label)
}

function presetWorkflowName(preset?: WorkflowPreset): string {
  if (!preset) return ''
  return workflowNameFromPath(preset.selectedFolder?.filepath) || preset.label
}

function terminalWorkflowKeys(terminal: TerminalSnapshot): string[] {
  return [
    workflowMatchKey(terminal.workflow_path),
    normalizeWorkflowIdentity(terminal.workflow_name),
    normalizeWorkflowIdentity(terminal.workflow_label),
  ].filter(Boolean)
}

export function terminalMatchesWorkflowPreset(
  terminal: TerminalSnapshot,
  preset: WorkflowPreset,
): boolean {
  const targetKey = presetWorkflowKey(preset)
  if (!targetKey) return false
  return terminalWorkflowKeys(terminal).includes(targetKey)
}

export function isLiveWorkflowTerminal(terminal: TerminalSnapshot): boolean {
  const state = (terminal.state || '').toLowerCase().trim()
  if (typeof terminal.tmux_session === 'string' && terminal.tmux_session.trim() !== '') {
    return state !== 'stale' && state !== 'failed'
  }
  return terminal.active === true ||
    state === 'running' ||
    state === 'active' ||
    state === 'in_progress'
}

function terminalStatusForRestore(terminal: TerminalSnapshot): string {
  const state = (terminal.state || '').toLowerCase().trim()
  if (['running', 'active', 'in_progress', 'paused', 'waiting', 'waiting_feedback', 'idle'].includes(state)) {
    return state
  }
  if (terminal.active) return 'running'
  return terminal.tmux_session?.trim() ? 'idle' : 'running'
}

function terminalSortRank(terminal: TerminalSnapshot): number {
  const state = (terminal.state || '').toLowerCase().trim()
  if (terminal.active) return 0
  if (state === 'running' || state === 'active' || state === 'in_progress') return 1
  if (terminal.tmux_session?.trim()) return 2
  return 3
}

function terminalTimestamp(terminal: TerminalSnapshot): number {
  return Date.parse(terminal.updated_at || terminal.created_at || '') || 0
}

export function sortLiveWorkflowTerminals(terminals: TerminalSnapshot[]): TerminalSnapshot[] {
  return [...terminals].sort((a, b) => {
    const rankDelta = terminalSortRank(a) - terminalSortRank(b)
    if (rankDelta !== 0) return rankDelta
    return terminalTimestamp(b) - terminalTimestamp(a)
  })
}

export function activeSessionFromWorkflowTerminal(
  terminal: TerminalSnapshot,
  options: {
    preset?: WorkflowPreset
    title?: string
  } = {},
): ActiveSessionInfo {
  const now = new Date().toISOString()
  const status = terminalStatusForRestore(terminal)
  const fallbackWorkflowName = workflowNameFromPath(terminal.workflow_path)
  const title = options.title ||
    terminal.workflow_label ||
    terminal.workflow_name ||
    options.preset?.label ||
    fallbackWorkflowName ||
    'Automation'

  return {
    session_id: terminal.session_id,
    observer_id: terminal.session_id,
    agent_mode: 'workflow',
    status,
    last_activity: terminal.updated_at || now,
    created_at: terminal.created_at || now,
    title,
    workflow_name: terminal.workflow_name || presetWorkflowName(options.preset) || fallbackWorkflowName,
    workflow_label: terminal.workflow_label || title,
    workspace_path: terminal.workflow_path || options.preset?.selectedFolder?.filepath,
    preset_name: options.preset?.label,
    preset_query_id: options.preset?.id,
    has_retained_tmux_session: Boolean(terminal.tmux_session?.trim()),
    current_execution_name: terminal.step_name || terminal.display_title || terminal.label,
  }
}

export function liveWorkflowTerminalSessionForPreset(
  terminals: TerminalSnapshot[],
  preset: WorkflowPreset,
  title: string,
): ActiveSessionInfo | undefined {
  const terminal = sortLiveWorkflowTerminals(terminals)
    .find(candidate =>
      candidate.session_id &&
      isMainAgentTerminal(candidate) &&
      terminalMatchesWorkflowPreset(candidate, preset) &&
      isLiveWorkflowTerminal(candidate)
    )
  return terminal ? activeSessionFromWorkflowTerminal(terminal, { preset, title }) : undefined
}
