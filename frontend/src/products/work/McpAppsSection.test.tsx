// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  list: vi.fn(),
  save: vi.fn(),
  remove: vi.fn(),
}))
vi.mock('../../api/mcpApps', () => ({ mcpAppsApi: api }))
import { McpAppsSection } from './McpAppsSection'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const REDIRECT = 'https://agents.example.com/api/oauth/callback'
const flush = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })

describe('Sign-in apps card (admin)', () => {
  it('fills the client from the downloaded JSON, warns about a missing redirect, and saves', async () => {
    api.list.mockResolvedValue({ apps: [{ key: 'google', label: 'Google', servers: ['GoogleDrive', 'GoogleGmail'], configured: false, required: true }], redirectUri: REDIRECT })
    api.save.mockResolvedValue(undefined)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<McpAppsSection />))
      await flush()
      // Minimized by default, with what needs setup in the summary line.
      expect(host.textContent).toContain('Google not set up')
      expect(host.textContent).not.toContain('Create credentials')
      await act(async () => (Array.from(host.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Manage')!).click())
      expect(host.textContent).toContain('Not set up')
      await act(async () => (Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Google'))!).click())
      expect(host.textContent).toContain(REDIRECT)

      const file = new File([JSON.stringify({ web: { client_id: '1.apps.googleusercontent.com', client_secret: 'GOCSPX-test', redirect_uris: ['https://other.example.com/cb'] } })], 'client_secret_1.json', { type: 'application/json' })
      const input = host.querySelector('input[type="file"]') as HTMLInputElement
      Object.defineProperty(input, 'files', { value: [file] })
      await act(async () => { input.dispatchEvent(new Event('change', { bubbles: true })) })
      await flush()
      expect((host.querySelector('input[aria-label="Google client ID"]') as HTMLInputElement).value).toBe('1.apps.googleusercontent.com')
      expect(host.textContent).toContain('does not list')

      await act(async () => (Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Save app'))!).click())
      await flush()
      expect(api.save).toHaveBeenCalledWith('google', '1.apps.googleusercontent.com', 'GOCSPX-test')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('stays one line once every provider is set up, and shows the steps only to replace', async () => {
    api.list.mockResolvedValue({ apps: [{ key: 'google', label: 'Google', servers: ['GoogleGmail'], configured: true, client_id: '1.apps.googleusercontent.com', required: true }], redirectUri: REDIRECT })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<McpAppsSection />))
      await flush()
      expect(host.textContent).toContain('Google set up')
      expect(host.textContent).not.toContain('Create credentials')
      expect(host.textContent).not.toContain(REDIRECT)
      const click = async (label: string) => act(async () => (Array.from(host.querySelectorAll('button')).find(b => b.textContent?.trim().startsWith(label))!).click())
      await click('Manage')
      await click('Google')
      expect(host.textContent).toContain('1.apps.googleusercontent.com')
      expect(host.textContent).not.toContain('Create credentials')
      await click('Replace')
      expect(host.textContent).toContain('Create credentials')
      expect(host.textContent).toContain(REDIRECT)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('renders nothing when no provider needs an app', async () => {
    api.list.mockResolvedValue({ apps: [], redirectUri: REDIRECT })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<McpAppsSection />))
      await flush()
      expect(host.querySelector('[data-testid="mcp-apps-section"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
