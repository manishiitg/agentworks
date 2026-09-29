import { expect, it } from 'vitest'
import { groupServiceLabel, providerGroupLabel, providerGroups } from './mcpGroups'

it('groups catalog servers that share a sign-in, and names them', () => {
  const groups = providerGroups([
    { name: 'googlegmail', catalog: 'GoogleGmail', sign_in: true, needs_client: false, group: 'google' },
    { name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false, group: 'google' },
    { name: 'slack', catalog: 'Slack', sign_in: true, needs_client: false, group: 'slack' },
    { name: 'linear', catalog: 'Linear', sign_in: true, needs_client: false },
  ])
  expect([...groups.keys()]).toEqual(['google'])
  expect(groups.get('google')?.map(entry => entry.catalog)).toEqual(['GoogleGmail', 'GoogleDrive'])
  expect(providerGroupLabel('google')).toBe('Google Workspace')
  expect(groupServiceLabel('GoogleGmail', 'google')).toBe('Gmail')
})
