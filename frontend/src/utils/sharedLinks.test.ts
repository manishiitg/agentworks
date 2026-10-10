// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { consumeSharedReturnPath, isShareableAppOrigin, rememberSharedReturnPath, repairLocalAppLink, sharedLink, sharedReportLink, sharedReturnPath, SHARE_RETURN_KEY } from './sharedLinks'

describe('Shared file links', () => {
  beforeEach(() => {
    const storage = (): Storage => {
      const values = new Map<string, string>()
      return {
        get length() { return values.size },
        clear: () => values.clear(),
        getItem: key => values.get(key) ?? null,
        key: index => [...values.keys()][index] ?? null,
        removeItem: key => { values.delete(key) },
        setItem: (key, value) => { values.set(key, String(value)) },
      }
    }
    vi.stubGlobal('sessionStorage', storage())
    vi.stubGlobal('localStorage', storage())
  })

  it('preserves Unicode, spaces and query-sensitive characters', () => {
    const path = 'Workflow/testing/db/assets/日本 report + &.pdf'
    const url = new URL(sharedLink('http://127.0.0.1:18743', 'file', path))
    const decoded = new TextDecoder().decode(Uint8Array.from(atob(url.searchParams.get('path')!), c => c.charCodeAt(0)))
    expect(decoded).toBe(path)
    expect(url.searchParams.has('token')).toBe(false)
    expect(url.origin).toBe('http://127.0.0.1:18743')
  })

  it('links directly to one report document', () => {
    const url = new URL(sharedReportLink(
      'http://127.0.0.1:18743',
      'Chats/Work/projects/demo',
      'db/reports/investments.html',
      'work-user',
    ))
    const decoded = new TextDecoder().decode(Uint8Array.from(atob(url.searchParams.get('path')!), c => c.charCodeAt(0)))
    expect(url.pathname).toBe('/report')
    expect(decoded).toBe('Chats/Work/projects/demo')
    expect(url.searchParams.get('document')).toBe('db/reports/investments.html')
    expect(url.searchParams.get('uid')).toBe('work-user')
  })

  it('does not present loopback previews as shareable deployments', () => {
    expect(isShareableAppOrigin('http://127.0.0.1:51733')).toBe(false)
    expect(isShareableAppOrigin('http://localhost:51733')).toBe(false)
    expect(isShareableAppOrigin('http://app.localhost:51733')).toBe(false)
    expect(isShareableAppOrigin('https://work.example.com')).toBe(true)
  })
  it('only restores internal asset pages after OAuth', () => {
    expect(sharedReturnPath('/file?path=YWJj')).toBe('/file?path=YWJj')
    expect(sharedReturnPath('/file/YWJj')).toBe('/file/YWJj')
    for (const value of ['//evil.com/file?path=x', '/\\evil.com/file?path=x', 'https://evil.com/file?path=x', '/admin?path=x', '/file']) expect(sharedReturnPath(value)).toBeNull()
  })

  it('restores the share URL from the OAuth-state fallback when the session context is replaced', () => {
    const target = '/file?path=V29ya2Zsb3cvZXhhbXBsZS9yZXBvcnQucGRm&uid=user-1'
    rememberSharedReturnPath(target, 'oauth-state-1')
    sessionStorage.removeItem(SHARE_RETURN_KEY)

    expect(consumeSharedReturnPath('oauth-state-1')).toBe(target)
    expect(consumeSharedReturnPath('oauth-state-1')).toBeNull()
  })

  it('does not restore another OAuth flow or an unsafe destination', () => {
    rememberSharedReturnPath('/file?path=YWJj', 'oauth-state-1')
    sessionStorage.removeItem(SHARE_RETURN_KEY)

    expect(consumeSharedReturnPath('oauth-state-2')).toBeNull()
    rememberSharedReturnPath('https://evil.example/file?path=YWJj', 'oauth-state-3')
    expect(consumeSharedReturnPath('oauth-state-3')).toBeNull()
  })
})

// A report link an agent wrote for a dead local port (PUBLIC_URL left at
// localhost:5173) opens on the app in use; other links are never changed.
describe('repairLocalAppLink', () => {
  it('moves only local share-page links to the current local origin', () => {
    const current = 'http://127.0.0.1:18743'
    expect(repairLocalAppLink('http://localhost:5173/report?path=V29yaw%3D%3D', current)).toBe('http://127.0.0.1:18743/report?path=V29yaw%3D%3D')
    expect(repairLocalAppLink('http://localhost:5173/settings', current)).toBe('http://localhost:5173/settings')
    expect(repairLocalAppLink('https://example.com/report?path=x', current)).toBe('https://example.com/report?path=x')
    expect(repairLocalAppLink('http://localhost:5173/report?path=x', 'https://project-a.example.com')).toBe('http://localhost:5173/report?path=x')
  })
})
