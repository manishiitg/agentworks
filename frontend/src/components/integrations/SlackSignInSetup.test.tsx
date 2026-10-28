// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { SlackSignInSetup } from './SlackSignInSetup'

const state = vi.hoisted(() => ({ admin: false, get: vi.fn(), put: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: state.get, put: state.put } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { is_admin: state.admin } }) }))
vi.mock('../workflow/bots/SlackSlugsSection', () => ({ slackErrorText: (_error: unknown, message: string) => message }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('does not load or expose administrator credentials to a non-admin', async () => {
  state.admin = false
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<SlackSignInSetup onSaved={async () => {}}/>))
    expect(state.get).not.toHaveBeenCalled()
    expect(host.childElementCount).toBe(0)
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('copies the server callback without saving, then saves through the existing admin API and clears the secret', async () => {
  state.admin = true
  const redirect = 'https://app.example.com/api/oauth/callback'
  state.get.mockResolvedValueOnce({ data: { redirect_uri: redirect, apps: [{ key: 'slack', configured: false, scopes: ['chat:write', 'im:read'] }] } })
  state.get.mockResolvedValue({ data: { redirect_uri: redirect, apps: [{ key: 'slack', configured: true, client_id: 'example-client', scopes: ['chat:write', 'im:read'] }] } })
  state.put.mockResolvedValue({ data: { saved: 'slack' } })
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  const saved = vi.fn(async () => {})
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  try {
    await act(async () => root.render(<SlackSignInSetup onSaved={saved}/>))
    expect(host.textContent).toContain('reuse the Slack app')
    expect(host.textContent).toContain('chat:write')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Copy Slack redirect URL"]')!.click())
    expect(writeText).toHaveBeenCalledWith(redirect)
    expect(state.put).not.toHaveBeenCalled()
    const fill = (input: HTMLInputElement, value: string) => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    await act(async () => {
      fill(host.querySelector<HTMLInputElement>('[aria-label="Slack client ID"]')!, 'example-client')
      fill(host.querySelector<HTMLInputElement>('input[type="password"]')!, 'fixture-secret')
    })
    await act(async () => host.querySelector<HTMLFormElement>('form')!.requestSubmit())
    expect(state.put).toHaveBeenCalledWith('/api/admin/mcp-apps/slack', { client_id: 'example-client', client_secret: 'fixture-secret' })
    expect(host.querySelector<HTMLInputElement>('input[type="password"]')!.value).toBe('')
    expect(saved).toHaveBeenCalledTimes(1)
    expect(host.textContent).toContain('Choose Connect my Slack')
    // A catalog-supplied client needs no separate editable app record.
    state.get.mockResolvedValue({ data: { redirect_uri: redirect, apps: [] } })
    await act(async () => root.render(<SlackSignInSetup key="built-in" onSaved={saved} ready/>))
    expect(host.querySelector('form')).toBeNull()
    expect(host.textContent).not.toContain('Ask AI')
  } finally { await act(async () => root.unmount()); host.remove(); vi.clearAllMocks(); vi.unstubAllGlobals() }
})
