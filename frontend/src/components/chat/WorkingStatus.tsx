import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { WORKING_ACCESSIBLE_LABEL, WORKING_GLYPH_FRAMES, WORKING_GLYPH_INTERVAL_MS, WORKING_GLYPH_STATIC, WORKING_TONE_COUNT, WORKING_WORDS, WORKING_WORD_INTERVAL_MS, formatElapsed, nextWorkingWordIndex, resolveWorkingLabel } from './workingWords'

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

// The twinkling star. Its own component so the quick frame changes re-render only the glyph. One subtle colour for
// every product; reduced motion shows a single still star.
function WorkingGlyph({ reduced }: { reduced: boolean }) {
  const [frame, setFrame] = useState(0)
  useEffect(() => {
    if (reduced) return
    const timer = window.setInterval(() => setFrame((index) => (index + 1) % WORKING_GLYPH_FRAMES.length), WORKING_GLYPH_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [reduced])
  return <span className="inline-block w-[1.1em] text-center text-[hsl(var(--working-tone))]" data-testid="working-glyph">{reduced ? WORKING_GLYPH_STATIC : WORKING_GLYPH_FRAMES[frame]}</span>
}

// One shared "the agent is working" line. Real server status wins; otherwise a friendly word that changes every few
// seconds, with the seconds since the server started the turn when it told us. Display only: it never decides anything.
// Screen readers get one stable "Working…" label; the changing text is hidden from them so they are not spammed.
export function WorkingStatus({ realStatus, startedAt, className }: { realStatus?: string; startedAt?: number; className?: string }) {
  const reduced = usePrefersReducedMotion()
  const recent = useRef<number[]>([])
  const [word, setWord] = useState(() => ({ index: nextWorkingWordIndex([]), step: 0 }))
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (reduced) return
    const timer = window.setInterval(() => setWord((current) => {
      recent.current = [...recent.current, current.index].slice(-16)
      return { index: nextWorkingWordIndex(recent.current), step: current.step + 1 }
    }), WORKING_WORD_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [reduced])
  useEffect(() => {
    if (startedAt === undefined) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [startedAt])
  const { text } = resolveWorkingLabel(realStatus, WORKING_WORDS[reduced ? 0 : word.index])
  const elapsed = startedAt !== undefined && now >= startedAt ? ` (${formatElapsed(now - startedAt)})` : ''
  // The colour moves through the working-tone theme tokens (warm to fresh), one step per new word; reduced motion keeps one.
  const toneStyle = { '--working-tone': `var(--working-tone-${reduced ? 0 : word.step % WORKING_TONE_COUNT})` } as CSSProperties
  return (
    <span className={className} style={toneStyle} data-testid="working-status">
      <span className="sr-only" role="status" aria-live="polite">{WORKING_ACCESSIBLE_LABEL}</span>
      <span aria-hidden="true">
        <WorkingGlyph reduced={reduced} />{' '}
        <span className="working-shimmer" data-testid="working-word">{text}</span>
        {elapsed ? <span className="text-muted-foreground">{elapsed}</span> : null}
      </span>
    </span>
  )
}
