import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { create } from 'zustand'
import { liveFeed } from '../../services/liveFeed'
import { Check, ListFilter } from 'lucide-react'
import { llmConfigService, type MenuFilter as SavedMenuFilter } from '../../services/llm-config-api'

// The Projects page filter: All, Mine or Shared with me, and a location. The server keeps the last choice for the
// person (menu_filter.go), so it survives a restart and is the same on every screen (owner, 10 Nov). Project groups
// are gone (owner, 22 Nov): pins and this filter cover them.

export type MenuFilter = 'all' | 'mine' | 'shared'

type Saved = { filter: MenuFilter; network: string }
const useMenuFilterStore = create<Saved & { loaded: boolean; error: string; set: (saved: Partial<Saved>) => void; fail: (error: string) => void }>()(set => ({
  filter: 'all', network: '', loaded: false, error: '',
  set: saved => set({ ...saved, loaded: true, error: '' }),
  fail: error => set({ error }),
}))

const fromServer = (result: SavedMenuFilter): Saved => ({ filter: result.filter === 'mine' || result.filter === 'shared' ? result.filter : 'all', network: result.network || '' })

let loading: Promise<void> | null = null
function loadFilter() {
  loading ??= llmConfigService.getMenuFilter()
    .then(result => useMenuFilterStore.getState().set(fromServer(result)))
    .catch(() => useMenuFilterStore.getState().fail('Could not load your filter.'))
    .finally(() => { loading = null })
  return loading
}
/** Saves a choice: the screen shows it at once and then what the server kept. */
async function saveFilter(change: Partial<Saved>) {
  const store = useMenuFilterStore.getState()
  const next = { filter: store.filter, network: store.network, ...change }
  store.set(next)
  try {
    useMenuFilterStore.getState().set(fromServer(await llmConfigService.setMenuFilter(next)))
  } catch {
    useMenuFilterStore.getState().fail('Could not save your filter. Try again.')
  }
}

export function useMenuFilter(enabled = true) {
  const state = useMenuFilterStore()
  useEffect(() => enabled ? liveFeed.subscribe(['menu_filter'], null, () => { void loadFilter() }) : undefined, [enabled])
  useEffect(() => { if (enabled && !state.loaded) void loadFilter() }, [enabled, state.loaded])
  return { error: state.error, filter: state.filter, network: state.network, pick: (next: MenuFilter, network?: string) => void saveFilter({ filter: next, ...(network === undefined ? {} : { network }) }) }
}

/** A project shared with the person: a Team copy on this Mac, or someone else's Crew on a server. */
export function isSharedProject(project: { owned_by_caller?: boolean; shared_line?: string }) {
  return project.owned_by_caller === false || Boolean(project.shared_line)
}

export function matchesMenuFilter(filter: MenuFilter, project: { owned_by_caller?: boolean; shared_line?: string }) {
  if (filter === 'mine') return !isSharedProject(project)
  if (filter === 'shared') return isSharedProject(project)
  return true
}

function usePopover() {
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null)
  useLayoutEffect(() => { if (position) panel.current?.querySelector('button')?.focus() }, [position])
  useEffect(() => {
    if (!position) return
    const outside = (event: MouseEvent) => {
      const target = event.target as Node
      if (!panel.current?.contains(target) && !trigger.current?.contains(target)) setPosition(null)
    }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); setPosition(null); trigger.current?.focus() } }
    const dismiss = () => setPosition(null)
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', dismiss)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', escape); window.removeEventListener('resize', dismiss) }
  }, [position])
  const toggle = (width: number, alignRight: boolean) => {
    if (position) { setPosition(null); return }
    const rect = trigger.current?.getBoundingClientRect()
    if (rect) setPosition({ top: Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - 260)), left: Math.max(8, Math.min(alignRight ? rect.right - width : rect.left, window.innerWidth - width - 8)) })
  }
  const render = (label: string, width: string, children: ReactNode) => position && createPortal(
    <div ref={panel} role="dialog" aria-label={label} style={{ ...position, maxHeight: `calc(100dvh - ${position.top + 8}px)` }} className={`fixed z-[100] max-w-[calc(100vw-16px)] overflow-y-auto overscroll-contain ${width} rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg`}>{children}</div>, document.body)
  return { trigger, open: !!position, close: () => setPosition(null), toggle, render }
}

const item = 'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

/** The Projects page's location filter; shown only when there is more than one location. */
export function ProjectFilterMenu({ locations, locationId, onLocation, onClear }: {
  locations: { id: string; name: string }[]
  locationId?: string
  onLocation: (id: string) => void
  onClear: () => void
}) {
  const { error } = useMenuFilter(false)
  const popover = usePopover()
  if (locations.length < 2) return null
  const count = Number(Boolean(locationId) && locationId !== 'all')
  const choose = (callback: () => void) => { callback(); popover.close(); popover.trigger.current?.focus() }
  const option = (name: string, selected: boolean, onClick: () => void, key?: string) => <button key={key} type="button" aria-pressed={selected} onClick={() => choose(onClick)} className={`${item} ${selected ? 'bg-muted' : ''}`}>
    <span className="min-w-0 flex-1 truncate">{name}</span>{selected && <Check aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />}
  </button>
  return <>
    <button ref={popover.trigger} type="button" aria-haspopup="dialog" aria-expanded={popover.open} onClick={() => popover.toggle(248, true)}
      className="flex h-9 shrink-0 items-center gap-2 rounded-lg border border-border bg-card px-2.5 text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <ListFilter aria-hidden="true" className="h-4 w-4 text-muted-foreground" />Filters
      {count > 0 && <span className="text-xs text-muted-foreground">{count}</span>}
    </button>
    {popover.render('Project filters', 'w-[248px]', <>
      <p className="px-2 pb-0.5 pt-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Location</p>
      {option('All locations', locationId === 'all', () => onLocation('all'))}
      {locations.map(location => option(location.name, locationId === location.id, () => onLocation(location.id), location.id))}
      {count > 0 && <button type="button" onClick={() => choose(onClear)} className={`${item} mt-1 border-t border-border text-primary`}>Clear filters</button>}
      {error && <p role="alert" className="px-2 py-1 text-[11px] text-destructive">{error}</p>}
    </>)}
  </>
}
