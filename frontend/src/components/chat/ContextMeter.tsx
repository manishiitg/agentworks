/** The one context bar: how full a chat is, amber past half. Used by the chat box chip, the model menu and the transcript footer. */
export function ContextMeter({ used, windowTokens, className = '', decorative = false }: { used: number; windowTokens: number; className?: string; decorative?: boolean }) {
  const percent = Math.min(100, Math.round((used / windowTokens) * 100))
  return <span className={`block h-1 overflow-hidden rounded-full bg-muted ${className}`}
    {...(decorative ? { 'aria-hidden': true } : { role: 'meter', 'aria-label': 'Context used', 'aria-valuemin': 0, 'aria-valuemax': windowTokens, 'aria-valuenow': used })}>
    <span className={`block h-full rounded-full ${percent > 50 ? 'bg-amber-500' : 'bg-primary'}`} style={{ width: `${Math.max(percent, 2)}%` }} />
  </span>
}
