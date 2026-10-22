import { Bot, Code2, Terminal, Wrench, type LucideIcon } from 'lucide-react'

export interface ExecutionModeVisuals {
  label: string | null
  title?: string
  Icon: LucideIcon | null
  iconBoxClassName: string
  iconClassName: string
  borderClassName: string
  handleClassName: string
  solidBadgeClassName: string
}

function normalizeExecutionMode(mode?: string): string | null {
  const normalized = mode?.trim().toLowerCase()
  if (!normalized) return null
  if (normalized === 'learn_code') return 'scripted'
  if (normalized === 'code_exec') return 'code_exec'
  return normalized
}

export function getExecutionModeLabel(mode?: string): string | null {
  switch (normalizeExecutionMode(mode)) {
    case 'scripted':
      return 'Scripted'
    case 'agentic':
      return 'Agentic'
    case 'code_exec':
      return 'Code exec'
    case 'tool_calling':
    case 'direct':
      return 'Direct'
    default:
      return mode ? mode.replace(/_/g, ' ') : null
  }
}

export function getExecutionModeVisuals(mode?: string): ExecutionModeVisuals {
  const normalized = normalizeExecutionMode(mode)
  const label = getExecutionModeLabel(mode)
  if (!normalized || !label) {
    return {
      label: null,
      title: undefined,
      Icon: null,
      iconBoxClassName: '',
      iconClassName: '',
      borderClassName: '',
      handleClassName: '',
      solidBadgeClassName: ''
    }
  }

  const title =
    normalized === 'scripted'
      ? 'Scripted execution: runs a saved reusable script when available'
      : normalized === 'agentic'
        ? 'Agentic execution: the agent decides actions during the run'
        : normalized === 'code_exec'
          ? 'Code execution mode'
          : `${label} execution mode`

  if (normalized === 'scripted') {
    return {
      label,
      title,
      Icon: Terminal,
      iconBoxClassName: 'bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 border border-emerald-200 dark:border-emerald-800',
      iconClassName: 'text-emerald-700 dark:text-emerald-300',
      borderClassName: 'border-emerald-500 dark:border-emerald-400',
      handleClassName: '!bg-emerald-500 dark:!bg-emerald-500',
      solidBadgeClassName: 'bg-emerald-600 dark:bg-emerald-700 text-white'
    }
  }

  if (normalized === 'agentic') {
    return {
      label,
      title,
      Icon: Bot,
      iconBoxClassName: 'bg-slate-100 dark:bg-slate-800/60 text-slate-700 dark:text-slate-300 border border-slate-200 dark:border-slate-700',
      iconClassName: 'text-slate-700 dark:text-slate-300',
      borderClassName: 'border-slate-400 dark:border-slate-500',
      handleClassName: '!bg-slate-500 dark:!bg-slate-500',
      solidBadgeClassName: 'bg-slate-700 dark:bg-slate-600 text-white'
    }
  }

  if (normalized === 'code_exec') {
    return {
      label,
      title,
      Icon: Code2,
      iconBoxClassName: 'bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-300 border border-amber-200 dark:border-amber-800',
      iconClassName: 'text-amber-700 dark:text-amber-300',
      borderClassName: 'border-amber-500 dark:border-amber-400',
      handleClassName: '!bg-amber-500 dark:!bg-amber-500',
      solidBadgeClassName: 'bg-amber-600 dark:bg-amber-700 text-white'
    }
  }

  return {
    label,
    title,
    Icon: Wrench,
    iconBoxClassName: 'bg-cyan-100 dark:bg-cyan-900/40 text-cyan-700 dark:text-cyan-300 border border-cyan-200 dark:border-cyan-800',
    iconClassName: 'text-cyan-700 dark:text-cyan-300',
    borderClassName: 'border-cyan-500 dark:border-cyan-400',
    handleClassName: '!bg-cyan-500 dark:!bg-cyan-500',
    solidBadgeClassName: 'bg-cyan-600 dark:bg-cyan-700 text-white'
  }
}
