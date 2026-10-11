import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, ExternalLink, X } from 'lucide-react'
import { offerNeedsReview, type McpOffer } from '../../api/mcpOffers'

const labels = { recurring: 'Free tier', trial: 'Trial credits', grant: 'Grant', open_source: 'Open source', unconfirmed: 'Needs review', paid: 'Paid API' }
export function McpOfferBadge({ offer, freshnessDays }: { offer: McpOffer; freshnessDays: number }) {
  const [open, setOpen] = useState(false)
  const [position, setPosition] = useState({ left: 16, top: 16 })
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const id = useId()
  const needsReview = offerNeedsReview(offer, freshnessDays)
  const label = needsReview ? 'Needs review' : labels[offer.kind]
  const tone = needsReview || offer.kind === 'trial' || offer.kind === 'grant'
    ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300'
    : offer.kind === 'paid' ? 'bg-muted text-muted-foreground' : 'bg-green-500/10 text-green-700 dark:text-green-300'
  useLayoutEffect(() => {
    if (!open) return
    const place = () => {
      if (!trigger.current || !panel.current) return
      const anchor = trigger.current.getBoundingClientRect()
      const rect = panel.current.getBoundingClientRect()
      const left = Math.max(16, Math.min(anchor.left, window.innerWidth - rect.width - 16))
      const top = anchor.bottom + rect.height + 8 < window.innerHeight - 16
        ? anchor.bottom + 8 : Math.max(16, anchor.top - rect.height - 8)
      setPosition({ left, top })
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => { window.removeEventListener('resize', place); window.removeEventListener('scroll', place, true) }
  }, [open])
  useEffect(() => {
    if (!open) return
    const close = () => { setOpen(false); trigger.current?.focus() }
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !panel.current?.contains(event.target) && !trigger.current?.contains(event.target)) setOpen(false)
    }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.stopPropagation(); close() } }
    document.addEventListener('pointerdown', outside)
    document.addEventListener('keydown', escape)
    panel.current?.querySelector<HTMLButtonElement>('button')?.focus()
    return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', escape) }
  }, [open])
  return <>
    <button ref={trigger} type="button" aria-label={`${offer.name} ${label} details`} aria-expanded={open} aria-controls={open ? id : undefined}
      aria-haspopup="dialog" onClick={() => setOpen(value => !value)} className={`inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-normal ${tone}`}>
      {label}<ChevronDown className="h-3 w-3" aria-hidden />
    </button>
    {open && createPortal(<div ref={panel} id={id} role="dialog" aria-label={`${offer.name} offer details`}
      style={position} className="fixed z-[100] max-h-[calc(100vh-2rem)] w-80 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-lg border border-border bg-background p-4 text-xs text-foreground shadow-xl">
      <div className="mb-3 flex items-center justify-between gap-3"><h3 className="text-sm font-medium">{offer.name} · {label}</h3>
        <button type="button" aria-label="Close offer details" onClick={() => { setOpen(false); trigger.current?.focus() }} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-3.5 w-3.5" /></button></div>
      <p className="font-medium">{offer.summary}</p>
      <p className="mt-2 text-muted-foreground">Provider API offer; MCP limits can differ.</p>
      {needsReview && <p className="mt-2 text-amber-700 dark:text-amber-300">This offer needs review. Check the official source before relying on it.</p>}
      {offer.conditions.length > 0 && <ul className="mt-3 list-disc space-y-1 pl-4 text-muted-foreground">{offer.conditions.map((condition, index) => <li key={index}>{condition}</li>)}</ul>}
      <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-[11px]">
        <a href={offer.sourceUrl} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">Official source<ExternalLink className="h-3 w-3" /></a>
        <span className="text-muted-foreground">{offer.lastVerified ? `Verified ${offer.lastVerified}` : offer.lastReviewed ? `Reviewed ${offer.lastReviewed}` : 'Not verified'}</span>
      </div>
    </div>, document.body)}
  </>
}
