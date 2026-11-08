import type { ReactNode } from 'react'
import { ArrowLeft, ChevronRight, RefreshCw, type LucideIcon } from 'lucide-react'

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

/*
 * The Workflow and Automations pages' look (owner-approved mock 8 Nov, docs/design/workflow-automations-pages-mock-20261108.html
 * in the deployments repo): a card per kind of thing with an Ask button, rows with an optional switch, a bold title,
 * up to two plain detail lines, one action and a chevron; a row opens in place. Every line is written by the server.
 */

/** A page card: title, one hint line and Ask (which posts the request into the Crew chat). */
export function PageCard({ title, hint, onAsk, onRefresh, refreshLabel = 'Refresh', refreshing, action, children, testId }: {
  title: string; hint?: ReactNode; onAsk?: () => void | Promise<void>; onRefresh?: () => void; refreshLabel?: string; refreshing?: boolean
  action?: ReactNode; children: ReactNode; testId?: string
}) {
  return <section aria-label={title} data-testid={testId} className="min-w-0 overflow-hidden rounded-xl border border-border bg-card">
    <div className="flex items-start gap-2 px-4 pt-3 pb-2">
      <div className="min-w-0 flex-1">
        <h3 className="text-sm font-semibold">{title}</h3>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </div>
      {action}
      {onRefresh && <button type="button" aria-label={refreshLabel} title={refreshLabel} onClick={onRefresh} className="grid h-6 w-6 shrink-0 place-items-center rounded-full border border-border text-muted-foreground hover:bg-muted hover:text-foreground">
        <RefreshCw className={`h-3 w-3 ${refreshing ? 'animate-spin' : ''}`} aria-hidden="true" />
      </button>}
      {onAsk && <button type="button" onClick={() => void onAsk()} className="shrink-0 rounded-full border border-border px-3 py-0.5 text-xs hover:bg-muted">Ask</button>}
    </div>
    {children != null && children !== false && <div className="divide-y divide-border border-t border-border">{children}</div>}
  </section>
}

/**
 * One row of a page card. `lead` is the left control (a Switch, a status dot or an icon), `lines` the plain detail
 * lines, `action` the one button on the right. With `children` the row opens in place.
 */
export function PageRow({ lead, title, lines, action, aside, open, onToggle, children, testId }: {
  lead?: ReactNode; title: ReactNode; lines?: ReactNode[]; action?: ReactNode; aside?: ReactNode
  open?: boolean; onToggle?: () => void; children?: ReactNode; testId?: string
}) {
  const expandable = children !== undefined && onToggle !== undefined
  return <div data-testid={testId} className="min-w-0">
    <div className="flex min-w-0 items-center gap-3 px-4 py-3">
      {lead && <span className="flex shrink-0 items-center">{lead}</span>}
      <button type="button" disabled={!expandable} aria-expanded={expandable ? open : undefined} onClick={onToggle} className="min-w-0 flex-1 text-left disabled:cursor-default">
        <span className="block truncate text-sm font-semibold">{title}</span>
        {lines?.filter(Boolean).map((line, index) => <span key={index} className="block truncate text-xs text-muted-foreground">{line}</span>)}
      </button>
      {aside && <span className="shrink-0 text-right text-xs text-muted-foreground">{aside}</span>}
      {action && <span className="shrink-0">{action}</span>}
      {expandable && <button type="button" aria-label={open ? 'Close details' : 'Open details'} onClick={onToggle} className="shrink-0 text-muted-foreground hover:text-foreground">
        <ChevronRight aria-hidden="true" className={`h-4 w-4 transition-transform ${open ? 'rotate-90' : ''}`} />
      </button>}
    </div>
    {open && children !== undefined && <div className="space-y-1.5 border-t border-border bg-muted/20 px-4 py-3 text-xs">{children}</div>}
  </div>
}

/** One "When · …" line inside an opened row. */
export function DetailLine({ label, children }: { label: string; children: ReactNode }) {
  return <p className="flex min-w-0 flex-wrap gap-x-2"><span className="w-14 shrink-0 font-semibold">{label}</span><span className="min-w-0 flex-1 text-muted-foreground">{children}</span></p>
}

/** The small button on a row's right ("Run now", "Stop", "Catch up", "Copy address"). */
export function RowButton({ children, onClick, disabled, primary, title }: { children: ReactNode; onClick?: () => void; disabled?: boolean; primary?: boolean; title?: string }) {
  return <button type="button" title={title} disabled={disabled} onClick={onClick}
    className={`rounded-full px-3 py-1 text-xs font-medium disabled:opacity-50 ${primary ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'border border-border hover:bg-muted'}`}>{children}</button>
}

/** A run's status dot: running, finished, stopped or not reached. */
export function RunDot({ state }: { state: 'running' | 'finished' | 'stopped' | 'idle' }) {
  const colour = { running: 'bg-amber-400', finished: 'bg-emerald-500', stopped: 'bg-red-500', idle: 'bg-muted-foreground/40' }[state]
  return <span aria-hidden="true" className={`inline-block h-2.5 w-2.5 rounded-full ${colour}`} />
}

/**
 * The top of a Crew page, in Share's shape (owner, 8 Nov): no title row, a back arrow at the left and one big switch
 * whose buttons carry a name and a short hint. The pane's own header is hidden for pages that use it.
 */
export function PageTabs<Id extends string>({ tabs, value, onChange, onBack, label }: {
  tabs: ReadonlyArray<{ id: Id; label: string; hint: string }>; value: Id; onChange: (next: Id) => void; onBack?: () => void; label: string
}) {
  return <div className="flex items-stretch gap-2">
    {onBack && <button type="button" aria-label="Back to the project menu" title="Back" onClick={onBack} className="grid w-8 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground">
      <ArrowLeft className="h-4 w-4" aria-hidden="true" />
    </button>}
    <nav aria-label={label} className="grid min-w-0 flex-1 gap-1 rounded-xl border border-border bg-muted/30 p-1" style={{ gridTemplateColumns: `repeat(${tabs.length}, minmax(0, 1fr))` }}>
      {tabs.map(tab => <button key={tab.id} type="button" aria-current={value === tab.id ? 'page' : undefined} onClick={() => onChange(tab.id)}
        className={`min-w-0 rounded-lg border px-2 py-2 text-center ${value === tab.id ? 'border-border bg-card text-foreground' : 'border-transparent text-muted-foreground hover:bg-muted'}`}>
        <span className="block truncate text-sm font-semibold">{tab.label}</span>
        <span className="mt-0.5 block truncate text-[11px]">{tab.hint}</span>
      </button>)}
    </nav>
  </div>
}
