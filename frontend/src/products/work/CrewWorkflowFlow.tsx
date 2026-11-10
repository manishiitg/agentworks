import { useState } from 'react'
import type { RelaySourceNode } from '../../components/workflow/relayGraphAnnotations'

/** A workflow drawn as a flow (owner, 10 Nov, approved mock): steps in order, each "Choose a path" as labelled cases side by
 * side, loops and groups boxed, long groups folded. The nodes are the server's outline; nothing here reads or runs code. */
const GROUP_FOLDS_AFTER = 8

const ring = (status?: string) => status === 'failed' ? 'border-red-500 text-red-500' : status === 'running' ? 'border-amber-400 text-amber-400' : status ? 'border-emerald-500 text-emerald-500' : 'border-border text-muted-foreground'

function Connector() { return <div aria-hidden="true" className="mx-auto h-3 w-px shrink-0 bg-border" /> }

export function WorkflowFlow({ nodes, statusOf, detailOf }: {
  nodes: RelaySourceNode[]
  statusOf: (node: RelaySourceNode) => string | undefined
  detailOf: (node: RelaySourceNode) => string
}) {
  const [open, setOpen] = useState<Set<string>>(new Set())
  const toggle = (id: string) => setOpen(current => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next })
  let counter = 0
  const numbers = new Map(nodes.filter(node => !node.role).map(node => [node.id, ++counter]))
  const children = (parent?: string) => nodes.filter(node => node.parent === parent)
  const stepsIn = (id: string): RelaySourceNode[] => children(id).flatMap(node => node.role ? stepsIn(node.id) : [node])

  const step = (node: RelaySourceNode) => <div key={node.id} className="mx-auto flex w-full max-w-xs items-start gap-2.5 rounded-xl border border-border bg-card px-3 py-2">
    <span aria-hidden="true" className={`mt-0.5 flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full border text-[11px] ${ring(statusOf(node))}`}>{numbers.get(node.id)}</span>
    <span className="min-w-0 text-left"><b className="block text-[13px] font-medium">{node.label}</b>{detailOf(node) && <span className="block text-xs text-muted-foreground">{detailOf(node)}</span>}</span>
  </div>

  const group = (node: RelaySourceNode) => {
    const inside = stepsIn(node.id)
    const folded = inside.length > GROUP_FOLDS_AFTER && !open.has(node.id)
    const failed = inside.some(step => statusOf(step) === 'failed')
    return <div key={node.id} className={`mx-auto w-full max-w-sm rounded-xl border border-dashed px-3 py-2 ${failed ? 'border-red-500/60' : 'border-border'}`}>
      <button type="button" aria-expanded={!folded} onClick={() => toggle(node.id)} className="flex w-full items-center gap-2 text-left text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span aria-hidden="true">{folded ? '▸' : '▾'}</span><span className="min-w-0 flex-1 truncate font-medium text-foreground/80">{node.label}</span><span>{inside.length} step{inside.length === 1 ? '' : 's'}</span>
      </button>
      {!folded && <div className="pt-2">{block(node.id)}</div>}
    </div>
  }

  const branch = (node: RelaySourceNode) => {
    const cases = children(node.id)
    return <div key={node.id} className="w-full">
      <div className="mx-auto w-full max-w-xs rounded-xl border border-dashed border-border bg-muted/30 px-3 py-1.5 text-center text-[13px] text-muted-foreground">{node.label}</div>
      <Connector />
      <div className="grid gap-3" style={{ gridTemplateColumns: `repeat(${Math.min(cases.length || 1, 3)}, minmax(0, 1fr))` }}>
        {cases.map(item => {
          const inside = stepsIn(item.id)
          const folded = inside.length > GROUP_FOLDS_AFTER && !open.has(item.id)
          return <div key={item.id} className="flex min-w-0 flex-col items-stretch border-t border-border">
            <button type="button" onClick={() => toggle(item.id)} aria-expanded={!folded} className="mx-auto -mt-2.5 mb-1 max-w-full truncate rounded-full border border-primary/40 bg-background px-2.5 text-[11.5px] text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{item.label}{folded ? ` · ${inside.length} steps` : ''}</button>
            {!folded && block(item.id)}
          </div>
        })}
      </div>
    </div>
  }

  const block = (parent?: string) => <div className="flex flex-col items-stretch">
    {children(parent).map((node, index) => <div key={node.id} className="flex flex-col items-stretch">
      {index > 0 && <Connector />}
      {!node.role ? step(node) : node.role === 'branch' ? branch(node) : group(node)}
    </div>)}
  </div>

  return <div className="px-4 py-4" data-testid="workflow-flow">{block()}</div>
}
