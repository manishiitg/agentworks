import { expect, it } from 'vitest'
import { normalizeProductChatFailure } from './productChatFailure'

it('reports a CLI stuck on its login screen as not signed in, not a connection problem (#252)', () => {
  const failure = normalizeProductChatFailure(
    'llm error: selected LLM failed: claude-code/claude-sonnet-5-5 [auth]: failed to clear stale Claude Code prompt draft "1. Claude account with subscription · Pro, Max, Team, or Enterprise": timed out waiting for Claude Code prompt draft to clear',
    { provider: 'claude-code' },
  )
  expect(failure.code).toBe('authentication_failed')
  expect(failure.title).toContain('not signed in')
  expect(failure.message).toContain('Setup → Models')
  expect(failure.retryable).toBe(false)
})
