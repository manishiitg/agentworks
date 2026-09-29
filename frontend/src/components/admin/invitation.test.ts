import { describe, expect, it } from 'vitest'
import { invitationText, inviteNotice } from './invitation'

describe('invitations', () => {
  it('words the invitation with the deployment, the address and the sign-in link', () => {
    expect(invitationText('Excellence Technologies', 'ana@gmail.com', 'https://agents.example.com'))
      .toBe('You have been added to Excellence Technologies. Open https://agents.example.com and sign in with Google using ana@gmail.com.')
  })
  it('tells the admin what happened, and offers the copy when no email went out', () => {
    expect(inviteNotice('sent', 'ana@gmail.com')).toEqual({ tone: 'ok', text: 'Invitation emailed to ana@gmail.com.' })
    expect(inviteNotice('not_configured', 'ana@gmail.com')?.tone).toBe('copy')
    expect(inviteNotice('exists', 'ana@gmail.com')?.tone).toBe('copy')
    const failed = inviteNotice('failed', 'ana@gmail.com', 'the email rate limit was reached; try again later')
    expect(failed?.tone).toBe('error')
    expect(failed?.text).toContain('rate limit')
    expect(inviteNotice(undefined, 'ana@gmail.com')).toBeNull()
  })
})
