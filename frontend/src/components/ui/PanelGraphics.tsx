import { ArrowRight, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'

type Tone = 'blue' | 'green' | 'amber' | 'rose' | 'muted'
const colors: Record<Tone, string> = {
  blue: 'border-sky-500/30 bg-sky-500/10 text-sky-600 dark:text-sky-300',
  green: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300',
  amber: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-300',
  rose: 'border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-300',
  muted: 'border-border bg-muted/50 text-muted-foreground',
}
export type PanelGraphicNode = { label: string; detail?: ReactNode; icon: LucideIcon; tone?: Tone }

function Node({ node }: { node: PanelGraphicNode }) {
  const Icon = node.icon
  return <div className="relative z-10 flex min-w-0 flex-col items-center gap-2 text-center">
    <span className={`flex h-11 w-11 items-center justify-center rounded-2xl border ring-4 ring-background ${colors[node.tone || 'blue']}`}><Icon className="h-5 w-5" aria-hidden="true" /></span>
    <div className="min-w-0 max-w-full"><p className="break-words text-xs font-medium">{node.label}</p>{node.detail !== undefined && <p className="mt-1 break-words text-[11px] text-muted-foreground">{node.detail}</p>}</div>
  </div>
}

/** A connection path, not an animation or a claim that work has run. */
export function PanelFlow({ label, nodes, paused = false }: { label: string; nodes: PanelGraphicNode[]; paused?: boolean }) {
  return <figure aria-label={label} className="rounded-xl bg-muted/20 px-3 py-5">
    <ol className="flex min-w-0 items-start gap-3">
      {nodes.map((node, index) => <li key={index} className="relative min-w-0 flex-1">
        {index < nodes.length - 1 && <div aria-hidden="true" className={`absolute left-1/2 right-[-50%] top-5 border-t-2 ${paused ? 'border-dashed border-border' : 'border-primary/30'}`}><ArrowRight className="absolute -right-1 -top-2 h-4 w-4 text-muted-foreground" /></div>}
        <Node node={node} />
      </li>)}
    </ol>
  </figure>
}

/** A parent with parallel children; unlike a flow, it implies no execution order. */
export function PanelConnections({ label, parent, branches }: { label: string; parent: PanelGraphicNode; branches: PanelGraphicNode[] }) {
  return <figure aria-label={label} className="rounded-xl bg-muted/20 px-3 py-4">
    <Node node={parent} />
    <div aria-hidden="true" className="mx-auto h-5 w-px bg-primary/30" />
    <ul className="flex min-w-0 items-start gap-3">
      {branches.map((node, index) => <li key={index} className="relative min-w-0 flex-1 pt-5">
        <div aria-hidden="true" className={`absolute top-0 h-px bg-primary/30 ${branches.length === 1 ? 'hidden' : index === 0 ? 'left-1/2 right-[-0.75rem]' : index === branches.length - 1 ? 'left-[-0.75rem] right-1/2' : 'left-[-0.75rem] right-[-0.75rem]'}`} />
        <div aria-hidden="true" className="absolute left-1/2 top-0 h-5 w-px bg-primary/30" />
        <Node node={node} />
      </li>)}
    </ul>
  </figure>
}

export function PanelCounters({ label, items }: { label: string; items: Array<PanelGraphicNode & { value: number }> }) {
  return <dl aria-label={label} className="grid grid-cols-3 gap-2">
    {items.map(item => <div key={item.label} className="min-w-0 rounded-xl border border-border bg-muted/20 p-3">
      <dt className="flex items-center gap-2 text-[11px] text-muted-foreground"><item.icon className="h-4 w-4 shrink-0" aria-hidden="true"/>{item.label}</dt>
      <dd className="mt-2 text-2xl font-semibold tabular-nums">{item.value}</dd>
    </div>)}
  </dl>
}

export function PanelStateBar({ label, items }: { label: string; items: Array<{ label: string; value: number; tone: Tone }> }) {
  const total = items.reduce((sum, item) => sum + item.value, 0)
  const fills: Record<Tone, string> = { blue: 'bg-sky-500', green: 'bg-emerald-500', amber: 'bg-amber-500', rose: 'bg-rose-500', muted: 'bg-muted-foreground/40' }
  return <figure aria-label={label} className="space-y-2 rounded-xl border border-border p-3">
    <div aria-hidden="true" className="flex h-3 overflow-hidden rounded-full bg-muted">{items.filter(item => item.value > 0).map(item => <span key={item.label} className={fills[item.tone]} style={{ width: `${item.value / total * 100}%` }} />)}</div>
    <figcaption className="flex flex-wrap gap-x-4 gap-y-1 text-xs">{items.map(item => <span key={item.label} className="flex items-center gap-1.5"><span aria-hidden="true" className={`h-2 w-2 rounded-full ${fills[item.tone]}`} /><span className="font-medium tabular-nums">{item.value}</span><span className="text-muted-foreground">{item.label}</span></span>)}</figcaption>
  </figure>
}
