import { describe, expect, it } from 'vitest'
import { parseOAuthClientJson } from './oauthClientJson'

describe('parseOAuthClientJson', () => {
  it('reads a Google web client file', () => {
    const parsed = parseOAuthClientJson(JSON.stringify({ web: { client_id: '1.apps.googleusercontent.com', client_secret: 'GOCSPX-x', project_id: 'p', redirect_uris: ['https://a.example.com/api/oauth/callback'] } }))
    expect(parsed).toEqual({ clientId: '1.apps.googleusercontent.com', clientSecret: 'GOCSPX-x', projectId: 'p', redirectUris: ['https://a.example.com/api/oauth/callback'] })
  })
  it('reads an installed-app file too', () => {
    expect(parseOAuthClientJson('{"installed":{"client_id":"a","client_secret":"b"}}')?.redirectUris).toEqual([])
  })
  it.each(['', 'not json', '[]', '{"web":{}}', '{"web":{"client_id":"a"}}', '{"other":{"client_id":"a","client_secret":"b"}}'])('rejects %j', (text) => {
    expect(parseOAuthClientJson(text)).toBeNull()
  })
})
