// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../services/api', () => ({ default: api }))
import { CLIOAuthConsent } from './CLIOAuthConsent'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.resetAllMocks())
it('makes executor folder access explicit without promising remote workflow access', async () => {
  window.history.replaceState({}, '', `/oauth/cli?code=cli_verify_${'a'.repeat(64)}`)
  api.get.mockResolvedValue({ data: { client_name: 'AgentWorks CLI', scopes: ['devices:connect'], user_code: 'AAAAAAAA' } })
  api.post.mockResolvedValue({})
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(<CLIOAuthConsent />))
  try {
    expect(host.textContent).toContain('Only folders you explicitly select')
    expect(host.textContent).toContain('Read-only folders cannot be edited')
    expect(host.textContent).not.toContain('start or control runs')
    const allow = [...host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    await act(async () => allow.click())
    expect(api.post).toHaveBeenCalledWith(expect.stringContaining('/api/oauth/cli/consent'), { decision: 'approve' })
  } finally { await act(async () => root.unmount()) }
})
