import { describe, expect, it, vi } from 'vitest'

vi.mock('../services/api', () => ({ authApi: {}, getAuthToken: vi.fn(), setAuthToken: vi.fn(), clearAuthToken: vi.fn() }))
vi.mock('./useChatStore', () => ({ switchChatAccount: vi.fn(), migrateLegacyChatStateForVerifiedAccount: vi.fn() }))

import { signInFailureMessage } from './useAuthStore'

describe('signInFailureMessage', () => {
  it('explains a refused sign-in instead of the bare status code', () => {
    const refused = Object.assign(new Error('Request failed with status code 403'), {
      response: { status: 403, data: { error: 'This account has not been added by an administrator' } },
    })
    expect(signInFailureMessage(refused)).toMatch(/not been added to this workspace.*administrator/)
    const disabled = { response: { status: 403, data: { error: 'This account is disabled' } } }
    expect(signInFailureMessage(disabled)).toMatch(/turned off/)
  })
  it('keeps the server reason for other failures', () => {
    expect(signInFailureMessage({ response: { status: 400, data: { error: 'Invalid or expired state parameter' } } })).toBe('Invalid or expired state parameter')
  })
})
