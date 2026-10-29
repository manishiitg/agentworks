/** The one token-count format: 950, 36.4k, 312k, 1.2M, 2B. Missing or negative counts read as 0. */
export function formatTokens(value?: number | null): string {
  const v = Math.max(0, Math.round(Number(value) || 0))
  if (v >= 1_000_000_000) return `${trim(v / 1_000_000_000)}B`
  if (v >= 1_000_000) return `${trim(v / 1_000_000)}M`
  if (v >= 1_000) return `${trim(v / 1_000)}k`
  return String(v)
}

function trim(x: number): string {
  return x >= 100 ? x.toFixed(0) : x.toFixed(1).replace(/\.0$/, '')
}
