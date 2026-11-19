import { useEffect, useRef, useState } from 'react'
import { joinFeature, leaveFeature, useCrewFeatures } from './featuresApi'

/**
 * ◈ on an agent's top bar (#3210): the features this agent is in, and the way to start or join one. Type a name: a
 * new name starts a feature, an existing one joins it. The server decides; this sends the name and shows its answer.
 */
export function FeatureChip({ projectId, tabId, main }: { projectId: string; tabId?: string; main: boolean }) {
  const { target, features, forTab, chatKeyFor } = useCrewFeatures(projectId)
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])
  const chat = chatKeyFor(tabId, main)
  if (!target || !chat) return null
  const mine = forTab(tabId, main)
  const others = features.filter(feature => !mine.includes(feature))
  const typed = name.trim().toLowerCase()
  const matches = typed ? others.filter(feature => feature.name.toLowerCase().includes(typed)) : others
  const run = async (job: () => Promise<unknown>) => {
    setBusy(true); setNote('')
    try { await job(); setName('') } catch (e) { setNote(e instanceof Error ? e.message : 'Try again') } finally { setBusy(false) }
  }
  const label = mine.length ? mine[0].name + (mine.length > 1 ? ` +${mine.length - 1}` : '') : 'Feature'
  return <div ref={box} className="relative shrink-0">
    <button type="button" aria-expanded={open} aria-label={mine.length ? `Features: ${mine.map(f => f.name).join(', ')}` : 'Start or join a feature'} onClick={() => setOpen(value => !value)}
      className={`flex h-6 max-w-40 items-center gap-1 rounded-full border px-2 text-[11px] ${mine.length ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:text-foreground'}`}>
      <span aria-hidden="true">◈</span><span className="min-w-0 truncate">{label}</span>
    </button>
    {open && <div className="absolute left-0 top-full z-50 mt-1 w-64 rounded-xl border border-border bg-popover p-2 text-xs shadow-lg">
      <form onSubmit={event => { event.preventDefault(); if (name.trim() && !busy) void run(() => joinFeature(target, chat, name.trim())) }}>
        <input autoFocus aria-label="Feature name" value={name} maxLength={80} onChange={event => setName(event.target.value)} placeholder="Type a feature name"
          className="w-full rounded border border-border bg-background px-2 py-1 text-xs" />
        {name.trim() && !features.some(feature => feature.name.toLowerCase() === typed) && <button type="submit" disabled={busy} className="mt-1 w-full rounded px-2 py-1 text-left text-primary hover:bg-muted disabled:opacity-50">+ Start "{name.trim()}"</button>}
      </form>
      {mine.length > 0 && <div className="mt-2"><p className="px-1 text-[10px] text-muted-foreground">This agent is in</p>
        {mine.map(feature => <div key={feature.id} className="flex items-center gap-1 px-1 py-0.5"><span aria-hidden="true" className="text-primary">◈</span><span className="min-w-0 flex-1 truncate">{feature.name}</span>
          <button type="button" disabled={busy} onClick={() => void run(() => leaveFeature(target, chat, feature.id))} className="shrink-0 text-[10px] text-muted-foreground hover:text-foreground disabled:opacity-50">Leave</button></div>)}
      </div>}
      {matches.length > 0 && <div className="mt-2"><p className="px-1 text-[10px] text-muted-foreground">Join</p>
        {matches.slice(0, 8).map(feature => <button key={feature.id} type="button" disabled={busy} onClick={() => void run(() => joinFeature(target, chat, feature.name))}
          className="flex w-full items-center gap-1 rounded px-1 py-0.5 text-left hover:bg-muted disabled:opacity-50"><span aria-hidden="true" className="text-muted-foreground">◈</span><span className="min-w-0 flex-1 truncate">{feature.name}</span>{feature.link && <span className="shrink-0 text-[10px] text-muted-foreground">{feature.link}</span>}</button>)}
      </div>}
      {note && <p className="mt-1 px-1 text-[10px] text-destructive">{note}</p>}
    </div>}
  </div>
}
