import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { useContextualGuide } from '../../hooks/useContextualGuide'
import ModalPortal from '../ui/ModalPortal'
import { contextualGuideKey } from '../../utils/onboarding'

/** A small action-specific hint; expanding Help remains the user's choice. */
export function FirstVisitTip({ topic, title, children, body, enabled = true, onLearnMore }: {
  topic: string; title: string; body: string; children: ReactNode; enabled?: boolean; onLearnMore: () => void
}) {
  const anchor = useRef<HTMLDivElement>(null)
  const key = contextualGuideKey('providers', topic)
  const tip = useContextualGuide(key, anchor, enabled)
  const popup = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({ left: 12, top: 12, maxHeight: 320 })
  useLayoutEffect(() => {
    if (!tip.open) return
    const place = () => {
      const rect = anchor.current?.getBoundingClientRect()
      if (!rect) return
      const width = Math.min(320, window.innerWidth - 24)
      const height = popup.current?.getBoundingClientRect().height ?? 200
      const top = rect.bottom + height + 20 <= window.innerHeight ? rect.bottom + 8 : Math.max(12, rect.top - height - 8)
      setPosition({ left: Math.max(12, Math.min(rect.left, window.innerWidth - width - 12)), top, maxHeight: window.innerHeight - top - 12 })
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => { window.removeEventListener('resize', place); window.removeEventListener('scroll', place, true) }
  }, [tip.open, topic])
  useEffect(() => {
    if (!tip.open) return
    const closeOutside = (event: PointerEvent) => {
      if (!anchor.current?.contains(event.target as Node) && !popup.current?.contains(event.target as Node)) tip.dismiss()
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.stopPropagation(); tip.dismiss() }
    }
    document.addEventListener('pointerdown', closeOutside)
    document.addEventListener('keydown', escape, true)
    return () => {
      document.removeEventListener('pointerdown', closeOutside)
      document.removeEventListener('keydown', escape, true)
    }
  }, [tip.open, tip.dismiss])
  return <div ref={anchor} className="relative">
    {children}
    {tip.open && <ModalPortal><div ref={popup} style={position} role="dialog" aria-label={`${title} tip`} data-contextual-guide={key} className="fixed z-[9999] overflow-y-auto w-[min(20rem,calc(100vw-1.5rem))] rounded-xl border border-border bg-popover p-4 text-popover-foreground shadow-xl">
      <h3 className="text-sm font-semibold">{title}</h3>
      <p className="mt-2 text-sm leading-5 text-muted-foreground">{body}</p>
      <div className="mt-3 flex items-center gap-3">
        <button type="button" onClick={() => { tip.dismiss(); onLearnMore() }} className="text-xs font-medium text-primary hover:underline">Show full guide</button>
        <button type="button" onClick={tip.dismiss} className="rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground">Got it</button>
      </div>
    </div></ModalPortal>}
  </div>
}
