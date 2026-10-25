import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('ConnectorsBrowser card status', () => {
  // The dot semantics live in catalog.ts, shared with the gateway console.
  const catalog = readFileSync('src/components/connectors/catalog.ts', 'utf8')

  it('reserves flat grey for truly disconnected cards; connected-but-loading pulses', () => {
    // The not_loaded branch must pulse so a connected card can never read as "not connected".
    expect(catalog).toMatch(/not_loaded'\) return \{ dot: '[^']*animate-pulse'/)
    // The disconnected fallback stays flat grey.
    expect(catalog).toContain("return { dot: 'bg-gray-300 dark:bg-gray-600', title: 'Not connected' }")
  })
})
