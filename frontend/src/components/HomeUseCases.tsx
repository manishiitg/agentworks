import { useRef, useState } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { CREW_STARTER_TYPES } from '../products/work/crewStarterTypes'

const cases = [
  ...CREW_STARTER_TYPES.map(item => ({ id: item.id, surface: 'work' as ProductSurface, title: item.label, detail: item.example, prompt: item.purpose, icon: item.icon })),

]

export function HomeUseCases({ products, onChoose }: { products: ProductSurface[]; onChoose: (prompt: string) => void }) {
  const [selected, setSelected] = useState('')
  const rail = useRef<HTMLDivElement>(null)
  const visible = products.includes('work') ? [...cases].sort((a, b) => Number(b.id === 'cowork') - Number(a.id === 'cowork')) : []
  if (!visible.length) return null
  return <section aria-labelledby="home-use-cases">
    <div className="mb-2 flex items-center justify-between gap-3"><h2 id="home-use-cases" className="text-xs font-medium text-muted-foreground">Start with a use case</h2><div className="flex gap-1">
      <button type="button" aria-label="Previous use cases" onClick={() => rail.current?.scrollBy({ left: -500, behavior: 'smooth' })} className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-muted"><ChevronLeft aria-hidden="true" className="h-3.5 w-3.5" /></button>
      <button type="button" aria-label="More use cases" onClick={() => rail.current?.scrollBy({ left: 500, behavior: 'smooth' })} className="grid h-7 w-7 place-items-center rounded-md text-muted-foreground hover:bg-muted"><ChevronRight aria-hidden="true" className="h-3.5 w-3.5" /></button>
    </div></div>
    <div ref={rail} aria-label="Use cases" className="flex gap-2 overflow-x-auto overscroll-x-contain pb-2">
      {visible.map(item => <button type="button" key={item.id} aria-pressed={selected === item.id} onClick={() => { setSelected(item.id); onChoose(item.prompt) }} className={`flex w-60 shrink-0 flex-col rounded-xl border p-3 text-left hover:border-primary/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${selected === item.id ? 'border-primary/50 bg-primary/5' : 'border-border'}`}>
        <item.icon aria-hidden="true" className="h-4 w-4 text-muted-foreground" />
        <span className="mt-2 text-xs font-semibold">{item.title}</span><span className="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{item.detail}</span>
      </button>)}
    </div>
  </section>
}
