import { useEffect, useState } from 'react'
import { WORKING_ACCESSIBLE_LABEL, WORKING_WORDS, WORKING_WORD_INTERVAL_MS, formatElapsed, resolveWorkingLabel } from './workingWords'

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

// One shared "the agent is working" line. Real server status wins; otherwise a friendly word that changes every few
// seconds, with the seconds since the server started the turn when it told us. Display only: it never decides anything.
// Screen readers get one stable "Working…" label; the changing text is hidden from them so they are not spammed.
export function WorkingStatus({ realStatus, startedAt, className }: { realStatus?: string; startedAt?: number; className?: string }) {
  const reduced = usePrefersReducedMotion()
  const [wordIndex, setWordIndex] = useState(() => Math.floor(Math.random() * WORKING_WORDS.length))
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (reduced) return
    const timer = window.setInterval(() => setWordIndex((index) => (index + 1) % WORKING_WORDS.length), WORKING_WORD_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [reduced])
  useEffect(() => {
    if (startedAt === undefined) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [startedAt])
  const { text } = resolveWorkingLabel(realStatus, WORKING_WORDS[reduced ? 0 : wordIndex])
  const elapsed = startedAt !== undefined && now >= startedAt ? ` (${formatElapsed(now - startedAt)})` : ''
  return (
    <span className={className} data-testid="working-status">
      <span className="sr-only" role="status" aria-live="polite">{WORKING_ACCESSIBLE_LABEL}</span>
      <span aria-hidden="true">
        <span className={reduced ? '' : 'inline-block animate-spin [animation-duration:2.4s]'} data-testid="working-glyph">✽</span>{' '}
        {text}{elapsed}
      </span>
    </span>
  )
}
