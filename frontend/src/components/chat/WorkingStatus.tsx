import { Suspense, lazy, useContext, useEffect, useRef, useState } from 'react'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { WorkingTipsContext } from './workingTipsContext'
import { WORKING_ACCESSIBLE_LABEL, WORKING_GLYPH_FRAMES, WORKING_GLYPH_INTERVAL_MS, WORKING_GLYPH_STATIC, WORKING_WORDS, WORKING_WORD_INTERVAL_MS, formatElapsed, nextWorkingWordIndex, resolveWorkingLabel } from './workingWords'

// Tip selection and catalog loading are needed only while a turn is Working.
const WorkingTip = lazy(() => import('./WorkingTip'))

function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches)
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const query = window.matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => setReduced(query.matches)
    query.addEventListener?.('change', update)
    return () => query.removeEventListener?.('change', update)
  }, [])
  return reduced
}

// The twinkling star (text glyphs only; see workingWords). Its own component so the quick frame changes re-render only
// the glyph. It takes the working accent colour; reduced motion shows a single still star.
function WorkingGlyph({ reduced }: { reduced: boolean }) {
  const [frame, setFrame] = useState(0)
  useEffect(() => {
    if (reduced) return
    const timer = window.setInterval(() => setFrame((index) => (index + 1) % WORKING_GLYPH_FRAMES.length), WORKING_GLYPH_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [reduced])
  return <span className="inline-block w-[1.1em] text-center [font-variant-emoji:text] [font-family:ui-sans-serif,system-ui,'Segoe_UI_Symbol','DejaVu_Sans',sans-serif]" data-testid="working-glyph">{reduced ? WORKING_GLYPH_STATIC : WORKING_GLYPH_FRAMES[frame]}</span>
}

// One shared "the agent is working" line. Real server status wins; otherwise a friendly word that changes every few
// seconds, with time since the server last observed agent activity. Display only: it never decides anything.
// Announce meaningful server state changes, never the playful words or elapsed clock.
export function WorkingStatus({ realStatus, startedAt, lastOutputAt, phase, className }: { realStatus?: string; startedAt?: number; lastOutputAt?: number; phase?: { phase: string; text: string; since: number }; className?: string }) {
  const reduced = usePrefersReducedMotion()
  const phone = useMobileViewport()
  const tipsAllowed = useContext(WorkingTipsContext)
  const recent = useRef<number[]>([])
  const [word, setWord] = useState(() => ({ index: nextWorkingWordIndex([]) }))
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (reduced) return
    const timer = window.setInterval(() => setWord((current) => {
      recent.current = [...recent.current, current.index].slice(-16)
      return { index: nextWorkingWordIndex(recent.current) }
    }), WORKING_WORD_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [reduced])
  useEffect(() => {
    if (startedAt === undefined && lastOutputAt === undefined && phase === undefined) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [startedAt, lastOutputAt, phase?.since])
  // Only server state decides quiet/waiting. The clock uses the same last
  // output/tool time as server inactivity handling, never the phase start.
  // Any server text wins: a launch not yet running, a retry, or the tool the agent is running right now.
  const notYetRunning = phase !== undefined && phase.text !== ''
  const resolved = resolveWorkingLabel(realStatus, phone ? 'Working' : WORKING_WORDS[reduced ? 0 : word.index])
  const text = notYetRunning ? phase.text : resolved.text
  const accessibleText = notYetRunning ? phase.text : resolved.real ? resolved.text : WORKING_ACCESSIBLE_LABEL
  const elapsed = phase?.phase === 'waiting_for_you' || phase?.phase === 'quiet' || lastOutputAt === undefined || now < lastOutputAt
    ? '' : ` · ${formatElapsed(now - lastOutputAt)} since update`
  const durationTitle = startedAt !== undefined && now >= startedAt ? `Turn duration: ${formatElapsed(now - startedAt)}` : undefined
  return (
    <span className={`inline-flex min-w-0 max-w-full flex-col ${className ?? ''}`} data-testid="working-status">
      <span className="sr-only" role="status" aria-live="polite" aria-atomic="true">{accessibleText}</span>
      <span aria-hidden="true" className="truncate" title={durationTitle}>
        <span className="text-[hsl(var(--working-accent))]">
          <WorkingGlyph reduced={reduced} />{' '}
          <span data-testid="working-word">{text}</span>
        </span>
        {elapsed ? <span className="text-muted-foreground">{elapsed}</span> : null}
      </span>
      {tipsAllowed ? <Suspense fallback={null}><WorkingTip turnKey={startedAt} /></Suspense> : null}
    </span>
  )
}
