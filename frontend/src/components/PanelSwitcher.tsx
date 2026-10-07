import { useEffect, useMemo, useRef, useState } from 'react'
import { PanelRight, Search } from 'lucide-react'
import { usePanelSwitcherStore } from '../stores/usePanelSwitcherStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

/** ⌘/Ctrl+J: search the current product's right-panel views and open one. */
export function PanelSwitcher({ onClose }: { onClose: () => void }) {
  const surface = useProductSurfaceStore(state => state.productSurface)
  const entry = usePanelSwitcherStore(state => state.entries[surface])
  const setToolbarMinimized = usePanelSwitcherStore(state => state.setToolbarMinimized)
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => { inputRef.current?.focus() }, [])

  const panels = useMemo(() => {
    const q = query.trim().toLowerCase()
    const all = entry?.panels ?? []
    if (!q) return all
    return all
      .filter(panel => panel.label.toLowerCase().includes(q) || panel.id.toLowerCase().includes(q) || panel.group?.toLowerCase().includes(q))
      .sort((a, b) => Number(!a.label.toLowerCase().startsWith(q)) - Number(!b.label.toLowerCase().startsWith(q)))
  }, [entry, query])
  useEffect(() => { setSelected(0) }, [query])

  const choose = (id: string) => {
    onClose()
    setToolbarMinimized(false)
    entry?.open(id)
  }
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') { event.preventDefault(); onClose() }
    else if (event.key === 'ArrowDown') { event.preventDefault(); setSelected(i => Math.min(i + 1, panels.length - 1)) }
    else if (event.key === 'ArrowUp') { event.preventDefault(); setSelected(i => Math.max(i - 1, 0)) }
    else if (event.key === 'Enter' && panels[selected]) { event.preventDefault(); choose(panels[selected].id) }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]" onClick={onClose}>
      <div className="absolute inset-0 bg-black/50" />
      <div role="dialog" aria-modal="true" aria-label="Open a panel"
        className="relative w-[min(30rem,calc(100vw-2rem))] overflow-hidden rounded-xl border border-border bg-background text-foreground shadow-2xl"
        onClick={event => event.stopPropagation()}>
        <div className="flex items-center gap-3 border-b border-border px-4 py-3">
          <Search className="h-5 w-5 shrink-0 text-muted-foreground" />
          <input ref={inputRef} type="text" value={query} onChange={event => setQuery(event.target.value)} onKeyDown={onKeyDown}
            placeholder="Open a panel…" aria-label="Search panels"
            className="flex-1 bg-transparent text-sm placeholder:text-muted-foreground focus:outline-none" />
          <kbd className="hidden rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground sm:inline-flex">ESC</kbd>
        </div>
        <div className="max-h-[48vh] overflow-y-auto" role="listbox" aria-label="Panels">
          {panels.length === 0
            ? <div className="px-4 py-8 text-center text-sm text-muted-foreground">{entry ? 'No matching panel' : 'This page has no panels'}</div>
            : panels.map((panel, index) => (
              <div key={panel.id} role="option" aria-selected={index === selected}
                className={`flex cursor-pointer items-center gap-3 px-4 py-2.5 ${index === selected ? 'bg-accent' : 'hover:bg-muted/60'}`}
                onMouseEnter={() => setSelected(index)}
                onMouseDown={event => { event.preventDefault(); choose(panel.id) }}>
                <PanelRight className="h-4 w-4 shrink-0 text-muted-foreground" />
                <span className="flex-1 truncate text-sm font-medium">{panel.label}</span>
                {panel.group && <span className="text-[11px] text-muted-foreground">{panel.group}</span>}
              </div>
            ))}
        </div>
        <div className="flex gap-3 border-t border-border bg-muted/40 px-4 py-2 text-[11px] text-muted-foreground">
          <span><kbd className="rounded bg-muted px-1 py-0.5 text-[10px]">↑↓</kbd> navigate</span>
          <span><kbd className="rounded bg-muted px-1 py-0.5 text-[10px]">↵</kbd> open</span>
          <span><kbd className="rounded bg-muted px-1 py-0.5 text-[10px]">⌘K</kbd> switch work instead</span>
        </div>
      </div>
    </div>
  )
}
