import { afterEach, describe, expect, it } from 'vitest'
import {
  apiLogEntries,
  apiTimingPathFor,
  clearApiTimings,
  getApiTimings,
  recordApiTiming,
  sanitizeApiBody,
  summarizeApiTimings,
} from './apiTiming'

afterEach(() => {
  clearApiTimings()
})

describe('apiTimingPathFor', () => {
  it('strips origin and query string, keeping only the pathname', () => {
    expect(apiTimingPathFor('/api/workflows/manifests?x=1', 'https://trader.tectonicmarkets.com')).toBe(
      '/api/workflows/manifests'
    )
    expect(apiTimingPathFor('https://example.com/api/health', undefined)).toBe('/api/health')
  })

  it('falls back gracefully for an unparseable url', () => {
    expect(apiTimingPathFor(undefined, undefined)).toBe('(unknown)')
  })
})

describe('recordApiTiming / getApiTimings', () => {
  it('records entries and caps the ring buffer at 500', () => {
    for (let i = 0; i < 510; i++) {
      recordApiTiming({ method: 'GET', path: '/api/health', status: 200, durationMs: 10, timestamp: i })
    }
    expect(getApiTimings().length).toBe(500)
    // Oldest entries are dropped first.
    expect(getApiTimings()[0].timestamp).toBe(10)
  })
})

describe('summarizeApiTimings', () => {
  it('aggregates per endpoint and sorts by total time descending', () => {
    recordApiTiming({ method: 'GET', path: '/api/health', status: 200, durationMs: 50, timestamp: 1 })
    recordApiTiming({ method: 'GET', path: '/api/health', status: 200, durationMs: 150, timestamp: 2 })
    recordApiTiming({ method: 'GET', path: '/api/workflows/manifests', status: 200, durationMs: 5000, timestamp: 3 })
    recordApiTiming({ method: 'POST', path: '/api/query', status: 'error', durationMs: 300, timestamp: 4 })

    const { aggregates, recent } = summarizeApiTimings()

    expect(aggregates[0].endpoint).toBe('GET /api/workflows/manifests')
    expect(aggregates[0].calls).toBe(1)
    expect(aggregates[0].avgMs).toBe(5000)

    const health = aggregates.find(a => a.endpoint === 'GET /api/health')
    expect(health?.calls).toBe(2)
    expect(health?.avgMs).toBe(100)
    expect(health?.maxMs).toBe(150)

    const errored = aggregates.find(a => a.endpoint === 'POST /api/query')
    expect(errored?.errors).toBe(1)

    // Recent entries are sorted slowest-first.
    expect(recent[0].durationMs).toBe(5000)
  })
})

describe('sanitizeApiBody', () => {
  it('redacts password/token/secret/api-key fields at any depth, case-insensitively', () => {
    const result = sanitizeApiBody({
      username: 'manish',
      Password: 'hunter2',
      nested: { access_token: 'abc123', ApiKey: 'xyz', keep: 'this stays' },
    }) as Record<string, unknown>

    expect(result.username).toBe('manish')
    expect(result.Password).toBe('[redacted]')
    const nested = result.nested as Record<string, unknown>
    expect(nested.access_token).toBe('[redacted]')
    expect(nested.ApiKey).toBe('[redacted]')
    expect(nested.keep).toBe('this stays')
  })

  it('redacts sensitive fields inside arrays too', () => {
    const result = sanitizeApiBody([{ token: 'a' }, { token: 'b', ok: 1 }]) as Array<Record<string, unknown>>
    expect(result[0].token).toBe('[redacted]')
    expect(result[1].token).toBe('[redacted]')
    expect(result[1].ok).toBe(1)
  })

  it('truncates a payload larger than the cap instead of storing it whole', () => {
    const big = { data: 'x'.repeat(10000) }
    const result = sanitizeApiBody(big) as { truncated: boolean; preview: string }
    expect(result.truncated).toBe(true)
    expect(result.preview.length).toBeLessThanOrEqual(4000)
  })

  it('does not traverse the rest of a large response to produce a tiny preview', () => {
    let reads = 0
    const rows = Array.from({ length: 10_000 }, () => ({
      get content() { reads++; return 'x'.repeat(1000) },
      token: 'never-retain-this',
    }))
    const result = sanitizeApiBody({ rows }) as { truncated: boolean; preview: string }
    expect(reads).toBeLessThan(10)
    expect(result.truncated).toBe(true)
    expect(result.preview.length).toBeLessThanOrEqual(4000)
    expect(result.preview).not.toContain('never-retain-this')
  })

  it('bounds wide objects and numeric arrays as well as long text', () => {
    for (const value of [Object.fromEntries(Array.from({ length: 1000 }, (_, i) => [`k${i}`, i])), Array.from({ length: 1000 }, (_, i) => i)]) {
      const result = sanitizeApiBody(value) as { truncated: boolean; preview: string }
      expect(result.truncated).toBe(true)
      expect(result.preview.length).toBeLessThanOrEqual(4000)
    }
  })

  it('passes small, non-sensitive payloads through unchanged', () => {
    expect(sanitizeApiBody({ a: 1, b: 'two' })).toEqual({ a: 1, b: 'two' })
    expect(sanitizeApiBody(undefined)).toBeUndefined()
    expect(sanitizeApiBody(null)).toBeNull()
  })
})

describe('apiLogEntries', () => {
  it('returns the most recent entries, optionally filtered by path or method substring', () => {
    recordApiTiming({ method: 'GET', path: '/api/health', status: 200, durationMs: 10, timestamp: 1 })
    recordApiTiming({ method: 'POST', path: '/api/query', status: 200, durationMs: 20, timestamp: 2 })
    recordApiTiming({ method: 'GET', path: '/api/workflows/manifests', status: 200, durationMs: 30, timestamp: 3 })

    expect(apiLogEntries().length).toBe(3)
    expect(apiLogEntries('workflows').map(e => e.path)).toEqual(['/api/workflows/manifests'])
    expect(apiLogEntries('POST').map(e => e.path)).toEqual(['/api/query'])
    expect(apiLogEntries('nope')).toEqual([])
  })

  it('caps to the requested limit, keeping the most recent', () => {
    for (let i = 0; i < 10; i++) {
      recordApiTiming({ method: 'GET', path: '/api/health', status: 200, durationMs: 1, timestamp: i })
    }
    const limited = apiLogEntries(undefined, 3)
    expect(limited.length).toBe(3)
    expect(limited.map(e => e.timestamp)).toEqual([7, 8, 9])
  })
})
