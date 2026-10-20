import type { ReactNode } from 'react'
import { ChevronRight, type LucideIcon } from 'lucide-react'

/** One colour per kind of automation, so a person can tell them apart at a glance (owner, 2026-10-20). */
export type AutomationTone = 'violet' | 'amber' | 'emerald' | 'sky' | 'rose'

const TONE: Record<AutomationTone, string> = {
  violet: 'bg-violet-500/10 text-violet-600 dark:text-violet-300',
  amber: 'bg-amber-500/10 text-amber-600 dark:text-amber-300',
  emerald: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300',
  sky: 'bg-sky-500/10 text-sky-600 dark:text-sky-300',
  rose: 'bg-rose-500/10 text-rose-600 dark:text-rose-300',
}

export type AutomationStatus = 'on' | 'paused' | 'failing' | 'done' | 'idle'

const STATUS: Record<AutomationStatus, string> = {
  on: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
  paused: 'bg-muted text-muted-foreground',
  failing: 'bg-red-500/10 text-red-600 dark:text-red-400',
  done: 'bg-sky-500/10 text-sky-600 dark:text-sky-400',
  idle: 'bg-muted text-muted-foreground',
}

export function StatusPill({ status, label }: { status: AutomationStatus; label: string }) {
  return <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${STATUS[status]}`}>{label}</span>
}

export function IconTile({ icon: Icon, tone }: { icon: LucideIcon; tone: AutomationTone }) {
  return <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${TONE[tone]}`}><Icon className="h-4 w-4" aria-hidden="true" /></span>
}

/** The short "what is this page" card at the top, with the one way to add an item. */
export function AutomationIntro({ icon, tone, title, children, action }: { icon: LucideIcon; tone: AutomationTone; title: string; children: ReactNode; action?: ReactNode }) {
  return <section className="flex min-w-0 items-start gap-3 rounded-xl border border-border bg-muted/30 p-3">
    <IconTile icon={icon} tone={tone} />
    <div className="min-w-0 flex-1 space-y-1">
      <h3 className="text-sm font-semibold">{title}</h3>
      <p className="text-xs leading-relaxed text-muted-foreground">{children}</p>
    </div>
    {action && <div className="shrink-0">{action}</div>}
  </section>
}

/** One item: icon, name, a plain one-line summary, its status and when it last ran. Details open on click. */
export function AutomationRow({ icon, tone, title, summary, status, statusLabel, meta, open, onToggle, children, testId }: {
  icon: LucideIcon
  tone: AutomationTone
  title: ReactNode
  summary?: ReactNode
  status: AutomationStatus
  statusLabel: string
  meta?: ReactNode
  open: boolean
  onToggle: () => void
  children?: ReactNode
  testId?: string
}) {
  return <li data-testid={testId} className="min-w-0">
    <button type="button" aria-expanded={open} onClick={onToggle} className="flex w-full min-w-0 items-center gap-3 px-3 py-2.5 text-left hover:bg-muted/40">
      <IconTile icon={icon} tone={tone} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{title}</span>
        {summary && <span className="block truncate text-xs text-muted-foreground">{summary}</span>}
      </span>
      {meta && <span className="hidden shrink-0 text-[11px] text-muted-foreground sm:inline">{meta}</span>}
      <StatusPill status={status} label={statusLabel} />
      <ChevronRight aria-hidden="true" className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${open ? 'rotate-90' : ''}`} />
    </button>
    {open && children && <div className="space-y-3 border-t border-border bg-muted/20 px-3 py-3 text-xs">{children}</div>}
  </li>
}

export function AutomationListBox({ children }: { children: ReactNode }) {
  return <ul className="min-w-0 divide-y divide-border overflow-hidden rounded-xl border border-border">{children}</ul>
}

export function EmptyAutomations({ children }: { children: ReactNode }) {
  return <p className="rounded-xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">{children}</p>
}

export function relativeTime(value?: string): string {
  if (!value) return ''
  const at = new Date(value).getTime()
  if (Number.isNaN(at)) return ''
  const minutes = Math.round((Date.now() - at) / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}
