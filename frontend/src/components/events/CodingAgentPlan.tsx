import { useState } from 'react'
import { Check, ChevronDown, ChevronRight, Circle, CircleDot } from 'lucide-react'
import type { PollingEvent } from '../../services/api-types'
import { codingAgentPlan } from '../../../shared/session/transcript/codingAgentPlan'

/** One retained row, independent of tool/thinking disclosure preferences. */
export function CodingAgentPlan({ event }: { event: PollingEvent }) {
  const [open, setOpen] = useState(true)
  const plan = codingAgentPlan(event)
  if (!plan || !plan.items.length) return null
  const completed = plan.items.filter(item => item.status === 'completed').length
  return <section aria-label="Agent plan" className="my-1 rounded-lg border border-border px-3 py-2 text-xs">
    <button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)} className="flex w-full items-center gap-2 text-left text-muted-foreground">
      {open ? <ChevronDown aria-hidden="true" className="h-3.5 w-3.5" /> : <ChevronRight aria-hidden="true" className="h-3.5 w-3.5" />}
      <span className="font-medium text-foreground">Plan</span><span>{completed} of {plan.items.length} done</span>
    </button>
    {open && <ul className="mt-2 space-y-1.5">{plan.items.map(item => {
      const Icon = item.status === 'completed' ? Check : item.status === 'running' ? CircleDot : Circle
      return <li key={item.id} className="flex items-start gap-2">
        <Icon aria-hidden="true" className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${item.status === 'running' ? 'text-primary' : 'text-muted-foreground'}`} />
        <span className="sr-only">{item.status === 'completed' ? 'Done' : item.status === 'running' ? 'In progress' : 'Pending'}: </span>
        <span className={item.status === 'completed' ? 'text-muted-foreground line-through' : 'text-foreground'}>{item.text}</span>
      </li>
    })}</ul>}
  </section>
}
