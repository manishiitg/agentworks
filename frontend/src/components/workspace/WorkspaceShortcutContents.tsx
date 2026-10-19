import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowLeft, GripVertical, RotateCcw, Search, SlidersHorizontal } from 'lucide-react'
import { DEFAULT_CONTEXT_SHORTCUTS, useContextShortcuts } from '../../hooks/useContextShortcuts'
import { PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import type { ShortcutProduct } from '../../services/contextShortcuts'
import { collectContextShortcuts, contextShortcutOptions, type WorkspaceShortcut } from './workspaceShortcuts'
import { usePointerDrag } from '../../hooks/usePointerDrag'

export function WorkspaceShortcutContents({ product, legacy, children, available, moreTools, consolidateConnections = false }: {
  product: ShortcutProduct; legacy?: ShortcutProduct; children: ReactNode; available?: readonly WorkspaceShortcut[]; moreTools?: ReactNode; consolidateConnections?: boolean
}) {
  const preferences = useContextShortcuts(product, legacy)
  const options = contextShortcutOptions(children, available)
  const byID = new Map(options.map(item => [item.id, item]))
  const normalize = (ids: readonly string[]) => normalizeConnectionShortcuts(ids, consolidateConnections && byID.has('mcp'))
  const defaults = normalize([...DEFAULT_CONTEXT_SHORTCUTS[product], ...collectContextShortcuts(children).map(item => item.id)])
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<{ order: string[]; selected: Set<string>; reset: boolean }>({ order: [], selected: new Set(), reset: false })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const editingGeneration = useRef(0)
  const dragging = useRef<string | null>(null)
  const editor = useRef<HTMLDivElement>(null)
  const pointerDrag = usePointerDrag()
  useEffect(() => { pointerDrag.stop(); dragging.current = null }, [preferences.scope, editing, pointerDrag.stop])
  useEffect(() => { setEditing(false); setBusy(false); setError(''); editingGeneration.current++ }, [preferences.scope])
  useEffect(() => () => { editingGeneration.current++ }, [])
  const begin = async () => {
    const generation = ++editingGeneration.current
    setEditing(true); setBusy(true); setError('')
    const record = await preferences.load()
    if (generation !== editingGeneration.current) return
    if (record) {
      const selected = normalize(record.shortcuts ?? defaults)
      setDraft({ order: [...new Set([...selected, ...options.map(item => item.id)])], selected: new Set(selected), reset: record.shortcuts === null })
    }
    setBusy(false)
  }
  const cancel = () => { editingGeneration.current++; setEditing(false); setBusy(false); setError('') }
  const save = async () => {
    const generation = editingGeneration.current
    setBusy(true); setError('')
    try {
      await preferences.save(draft.reset ? null : draft.order.filter(id => draft.selected.has(id)))
      if (generation === editingGeneration.current) { setEditing(false); setBusy(false) }
    } catch (cause) {
      if (generation === editingGeneration.current) { setBusy(false); setError(cause instanceof Error ? cause.message : 'Could not save your shortcuts.') }
    }
  }
  const move = (id: string, target: string) => {
    if (busy || !byID.has(id) || !byID.has(target) || id === target) return
    setDraft(previous => {
      const order = [...previous.order], source = order.indexOf(id), destination = order.indexOf(target)
      if (source < 0 || destination < 0) return previous
      order.splice(source, 1); order.splice(destination, 0, id)
      return { ...previous, order, reset: false }
    })
    requestAnimationFrame(() => editor.current?.querySelector<HTMLButtonElement>(`[data-reorder="${id}"]`)?.focus())
  }
  const visibleDraft = draft.order.filter(id => byID.has(id))
  const saved = preferences.entry?.record?.shortcuts
  const selectedOptions = saved ? normalize(saved).map(id => byID.get(id)).filter((item): item is WorkspaceShortcut => Boolean(item)) : undefined
  const content = !selectedOptions ? children : selectedOptions.length ? <div className="space-y-1">
    {selectedOptions.map(item => <div key={item.id}>{item.content ?? <button type="button" onClick={() => byID.get(item.id)?.onClick?.()} className="flex w-full items-center gap-2.5 rounded-md px-1 py-1.5 text-left text-xs text-foreground/85 hover:bg-background/40"><item.icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span>{item.label}</span></button>}</div>)}
  </div> : <p className="py-3 text-xs text-muted-foreground">Add the shortcuts you want to keep here.</p>
  const openSearch = () => window.dispatchEvent(new CustomEvent('open-quick-switcher', { detail: { query: '@panels ' } }))
  return <>
    {editing && <header className="shrink-0 border-b border-border pb-3">
      <div className="flex items-center gap-2">
        <button type="button" aria-label="Cancel shortcut changes" title="Cancel shortcut changes" disabled={busy && preferences.entry?.status === 'ready'} onClick={cancel} className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"><ArrowLeft className="h-3.5 w-3.5" /></button>
        <h2 className="flex-1 text-sm font-semibold text-foreground">Edit shortcuts</h2>
        {!busy && <span className="text-[10px] text-muted-foreground">{visibleDraft.filter(id => draft.selected.has(id)).length} shown</span>}
      </div>
      <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{PRODUCT_SURFACE_LABELS[product]} · Just for you</p>
    </header>}
    {editing ? <div ref={editor} aria-label="Shortcut editor" aria-busy={busy} className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
      {preferences.entry?.status === 'loading' ? <p className="py-4 text-xs text-muted-foreground">Loading your shortcuts…</p> : preferences.entry?.status === 'error' ? <button type="button" onClick={() => void begin()} className="py-3 text-xs text-muted-foreground underline">Could not load your shortcuts · Retry</button> : <>
        <div>
        {visibleDraft.map((id, index) => {
          const item = byID.get(id)!, Icon = item.icon
          return <div key={id} data-shortcut-row={id} className="flex min-h-10 items-center gap-1 rounded-md hover:bg-background/30">
            <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2.5 px-1 py-2 text-xs">
              <input type="checkbox" checked={draft.selected.has(id)} disabled={busy} className="h-3.5 w-3.5 shrink-0 accent-primary"
                onChange={event => { const checked = event.target.checked; setDraft(previous => { const selected = new Set(previous.selected); if (checked) selected.add(id); else selected.delete(id); return { ...previous, selected, reset: false } }) }} />
              <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /><span>{item.label}</span>
            </label>
            <button type="button" data-reorder={id} aria-label={`Reorder ${item.label}`} title="Drag to reorder, or use the up and down arrow keys" disabled={busy}
              onPointerDown={event => {
                if (busy) return
                dragging.current = id
                pointerDrag.start(event, { onMove: y => {
                  const target = [...(editor.current?.querySelectorAll<HTMLElement>('[data-shortcut-row]') ?? [])].find(row => { const rect = row.getBoundingClientRect(); return y >= rect.top && y <= rect.bottom })?.dataset.shortcutRow
                  if (target) move(id, target)
                }, onEnd: () => { dragging.current = null } }, 'y')
              }}
              onKeyDown={event => { if (event.key === 'ArrowUp' || event.key === 'ArrowDown') { event.preventDefault(); const target = visibleDraft[index + (event.key === 'ArrowUp' ? -1 : 1)]; if (target) move(id, target) } }}
              className="grid h-8 w-7 shrink-0 touch-none cursor-grab place-items-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"><GripVertical className="h-3.5 w-3.5" /></button>
          </div>
        })}
        </div>
        <p className="mt-2 text-[10px] text-muted-foreground">Drag to reorder.</p>
      </>}
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
      {error.includes('Reload') && <button type="button" onClick={() => void begin()} className="mt-2 text-xs underline">Reload saved shortcuts</button>}
      </div>
      <div className="mt-3 flex shrink-0 items-center justify-between gap-2 border-t border-border pt-3">
        <button type="button" disabled={busy || preferences.entry?.status !== 'ready'} onClick={() => { setDraft({ order: [...new Set([...defaults, ...options.map(item => item.id)])], selected: new Set(defaults), reset: true }); setError('') }} className="flex items-center gap-1.5 rounded py-1.5 text-[11px] text-muted-foreground hover:text-foreground disabled:opacity-50"><RotateCcw className="h-3.5 w-3.5" />Reset to default</button>
        <button type="button" disabled={busy || preferences.entry?.status !== 'ready'} onClick={() => void save()} className="rounded-md bg-foreground px-3.5 py-1.5 text-xs font-medium text-background disabled:opacity-50">{busy ? preferences.entry?.status === 'loading' ? 'Loading…' : 'Saving…' : 'Done'}</button>
      </div>
    </div> : <>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain">
      {content}
      {moreTools}
      {preferences.entry?.status === 'error' && <p role="status" className="text-[11px] text-muted-foreground">Could not load saved shortcuts. <button type="button" onClick={() => void preferences.load()} className="underline">Retry</button></p>}
      </div>
      <div className="flex shrink-0 items-center justify-between gap-2 border-t border-border pt-3">
        {preferences.available && <button type="button" onClick={() => void begin()} className="flex items-center gap-1.5 rounded py-1 text-[11px] text-muted-foreground hover:text-foreground"><SlidersHorizontal className="h-3.5 w-3.5" />Edit shortcuts</button>}
        <button type="button" aria-label="All views" title="Find a panel" onClick={openSearch} className="ml-auto flex items-center gap-2 rounded px-1 py-1 text-xs text-muted-foreground hover:text-foreground"><Search className="h-3.5 w-3.5" /><kbd className="rounded border border-border px-1 py-0.5 text-[10px]">{typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl+K'}</kbd></button>
      </div>
    </>}
  </>
}

const LEGACY_CONNECTION_IDS = new Set(['connected', 'available', 'secrets', 'skills', 'vault', 'brain', 'folders', 'slack', 'whatsapp', 'gmail', 'cli'].map(id => `mcp:${id}`))
/** Presentation-only consolidation. Unavailable/unknown choices stay in the saved draft. */
export function normalizeConnectionShortcuts(ids: readonly string[], enabled: boolean) {
  return [...new Set(ids.map(id => enabled && LEGACY_CONNECTION_IDS.has(id) ? 'mcp' : id))]
}
