import { describe, expect, it } from 'vitest'
import { retryTurnRunningSubmission } from './turnRunningRetry'

// #3795: a retry after 409 turn_running rotates the Idempotency-Key but keeps
// the message's first key, so the stored row matches the bubble on screen.
describe('turn_running retry keeps the message identity', () => {
  it('sends the first key as X-Client-Message-ID on every retry', async () => {
    const headers: Record<string, unknown> = { 'Idempotency-Key': 'first-key' }
    const conflict = { response: { status: 409, data: { error: 'turn_running' } }, config: { headers } }
    const seen: Array<Record<string, unknown>> = []
    let calls = 0
    const instance = async (config: { headers?: unknown }) => {
      seen.push({ ...(config.headers as Record<string, unknown>) })
      calls += 1
      if (calls === 1) throw conflict
      return 'ok'
    }
    const result = await retryTurnRunningSubmission(instance as never, conflict, async () => {}).catch(error => retryTurnRunningSubmission(instance as never, error, async () => {}))
    expect(result).toBe('ok')
    expect(seen).toHaveLength(2)
    for (const sent of seen) {
      expect(sent['X-Client-Message-ID']).toBe('first-key')
      expect(sent['Idempotency-Key']).not.toBe('first-key')
    }
    expect(seen[0]['Idempotency-Key']).not.toBe(seen[1]['Idempotency-Key'])
  })
})
