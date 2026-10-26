import { useRef, useState } from 'react'
import { ChevronLeft, ChevronRight, CalendarClock, Target, Workflow } from 'lucide-react'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { PRODUCT_SURFACE_LABELS } from '../products/productSurfaceConfig'
import { CREW_STARTER_TYPES } from '../products/work/crewStarterTypes'

const cases = [
  ...CREW_STARTER_TYPES.map(item => ({ id: item.id, surface: 'work' as ProductSurface, title: item.label, detail: item.example, prompt: item.purpose, icon: item.icon })),
  { id: 'goal-progress', surface: 'agentworks' as ProductSurface, title: 'Work toward a target', detail: 'Plan the work and keep track of progress.', prompt: 'Help me set a goal, plan the work and track progress toward it.', icon: Target },
  { id: 'goal-monitor', surface: 'agentworks' as ProductSurface, title: 'Keep an eye on changes', detail: 'Follow a topic or a set of numbers over time.', prompt: 'Help me monitor something over time and tell me when it needs attention.', icon: CalendarClock },
  { id: 'flow-report', surface: 'relays' as ProductSurface, title: 'Prepare a weekly update', detail: 'Gather information, summarise it and send an update.', prompt: 'Help me automate a weekly update: gather the information, summarise it and deliver it.', icon: CalendarClock },
  { id: 'flow-process', surface: 'relays' as ProductSurface, title: 'Automate a repeated task', detail: 'Turn the steps you repeat into a reusable process.', prompt: 'Help me turn a repeated task into a process that my apps can call.', icon: Workflow },
]

export function HomeUseCases({ products, onChoose }: { products: ProductSurface[]; onChoose: (prompt: string) => void }) {
  const [selected, setSelected] = useState('')
  const rail = useRef<HTMLDivElement>(null)
  const groups = ['work', 'agentworks', 'relays'].filter(surface => products.includes(surface as ProductSurface))
    .map(surface => cases.filter(item => item.surface === surface).sort((a, b) => Number(b.id === 'cowork') - Number(a.id === 'cowork')))
  const visible = Array.from({ length: Math.max(0, ...groups.map(group => group.length)) }, (_, index) => groups.flatMap(group => group[index] ? [group[index]] : [] )).flat()
  if (!visible.length) return null
  return <section aria-labelledby="home-use-cases">
    <div className="mb-2 flex items-center justify-between gap-3"><h2 id="home-use-cases" className="text-xs font-medium text-muted-foreground">Start with a use case</h2><div className="flex gap-1">
      <button type="button" aria-label="Previous use cases" onClick={() => rail.current?.scrollBy({ left: -500, behavior: 'smooth' })} className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-muted"><ChevronLeft aria-hidden="true" className="h-3.5 w-3.5" /></button>
      <button type="button" aria-label="More use cases" onClick={() => rail.current?.scrollBy({ left: 500, behavior: 'smooth' })} className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-muted"><ChevronRight aria-hidden="true" className="h-3.5 w-3.5" /></button>
    </div></div>
    <div ref={rail} aria-label="Use cases" className="flex gap-2 overflow-x-auto overscroll-x-contain pb-2">
      {visible.map(item => <button type="button" key={item.id} aria-pressed={selected === item.id} onClick={() => { setSelected(item.id); onChoose(item.prompt) }} className={`flex w-60 shrink-0 flex-col rounded-xl border p-3 text-left hover:border-primary/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${selected === item.id ? 'border-primary/50 bg-primary/5' : 'border-border'}`}>
        <span className="flex w-full items-center justify-between gap-3"><item.icon aria-hidden="true" className="h-4 w-4 text-muted-foreground" /><span className="text-[10px] text-muted-foreground">{['code', 'cowork', 'personal'].includes(item.id) ? 'Studio' : PRODUCT_SURFACE_LABELS[item.surface]}</span></span>
        <span className="mt-2 text-xs font-semibold">{item.title}</span><span className="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{item.detail}</span>
      </button>)}
    </div>
  </section>
}
