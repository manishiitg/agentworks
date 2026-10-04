import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('ConnectorsBrowser card status', () => {
  const connectors = readFileSync('src/components/connectors/ConnectorsBrowser.tsx', 'utf8')
  // The dot semantics live in catalog.ts, shared with the gateway console.
  const catalog = readFileSync('src/components/connectors/catalog.ts', 'utf8')

  it('shows no per-card sharing pill — every connection is shared, so it carries no information', () => {
    expect(connectors).not.toContain('Shared platform connection')
  })

  it('reserves flat grey for truly disconnected cards; connected-but-loading pulses', () => {
    // The not_loaded branch must pulse so a connected card can never read as "not connected".
    expect(catalog).toMatch(/not_loaded'\) return \{ dot: '[^']*animate-pulse'/)
    // The disconnected fallback stays flat grey.
    expect(catalog).toContain("return { dot: 'bg-gray-300 dark:bg-gray-600', title: 'Not connected' }")
  })
})
