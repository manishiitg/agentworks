// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { status, connect, reconnect } = vi.hoisted(() => ({ status: vi.fn(), connect: vi.fn(), reconnect: vi.fn() }))
vi.mock('../../api/googleApp', () => ({ googleAppApi: { status, connect, reconnect } }))

import { GoogleAccountConnect } from './GoogleAccountConnect'
import { changeGoogleAccountAccess } from './googleAccountAccess'
import type { GmailConnection } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks(); document.body.innerHTML = '' })

const render = async (props: Partial<React.ComponentProps<typeof GoogleAccountConnect>> = {}) => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => act(() => root.unmount()))
  await act(async () => { root.render(<GoogleAccountConnect workspacePath="Chats/Code/projects/p1" {...props} />) })
  await act(async () => { await Promise.resolve() })
  return host
}

describe('GoogleAccountConnect', () => {
  it('keeps the existing connection and send-only access when changing a legacy account', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: '' })
    reconnect.mockResolvedValue({ id: 'legacy', auth_url: 'https://accounts.google.com/auth' })
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    try {
      const host = await render({ workspacePath: 'Workflow/support', privateAccount: false })
      const account = { id: 'legacy', email: 'me@example.com', allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }] } as GmailConnection
      await act(async () => changeGoogleAccountAccess(account, 'Workflow/other'))
      expect(host.textContent).not.toContain('Change access for')
      await act(async () => changeGoogleAccountAccess(account, 'Workflow/support'))
      expect((host.querySelector('[aria-label="Gmail access"]') as HTMLSelectElement).value).toBe('off')
      await act(async () => ([...host.querySelectorAll('button')].find(b => b.textContent === 'Sign in again with Google') as HTMLButtonElement).click())
      expect(reconnect).toHaveBeenCalledExactlyOnceWith('legacy', { workspace_path: 'Workflow/support', services: [{ service: 'docs', write: true }], allow_read_access: false, allow_agent_write_access: false })
      expect(connect).not.toHaveBeenCalled()
    } finally { open.mockRestore() }
  })

  it('blocks account creation and access changes for shared-account readers', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: '' })
    const host = await render({ workspacePath: 'Workflow/support', privateAccount: false, readOnly: true })
    expect(host.textContent).toContain('An administrator manages shared Google accounts')
    expect([...host.querySelectorAll('select')].every(select => select.disabled)).toBe(true)
    await act(async () => ([...host.querySelectorAll('button')].find(b => b.textContent === 'Connect Google account') as HTMLButtonElement).click())
    await act(async () => changeGoogleAccountAccess({ id: 'shared', email: 'me@example.com' } as GmailConnection, 'Workflow/support'))
    expect(host.textContent).not.toContain('Change access for')
    expect(connect).not.toHaveBeenCalled()
    expect(reconnect).not.toHaveBeenCalled()
  })

  it('shows nothing when the server has no Google app', async () => {
    status.mockResolvedValue({ configured: false, redirect_uri: '' })
    const host = await render()
    expect(host.querySelector('[data-testid="google-account-connect"]')).toBeNull()
  })

  it('connects with read-only defaults and opens Google, with no file to upload', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: 'https://app/api/oauth/callback' })
    connect.mockResolvedValue({ id: 'c1', auth_url: 'https://accounts.google.com/o/oauth2/auth?x=1' })
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const host = await render()
    expect(host.querySelector('input[type="file"]')).toBeNull()
    expect(host.textContent).toContain('never sees your password or token')
    await act(async () => { ([...host.querySelectorAll('button')].find(b => b.textContent?.includes('Connect Google account')) as HTMLButtonElement).click() })
    await act(async () => { await Promise.resolve() })
    expect(connect).toHaveBeenCalledWith({
      workspace_path: 'Chats/Code/projects/p1',
      services: [{ service: 'drive', write: false }, { service: 'calendar', write: false }],
      allow_read_access: true,
      allow_agent_write_access: false,
    })
    expect(open).toHaveBeenCalledWith('https://accounts.google.com/o/oauth2/auth?x=1', '_blank', 'noopener')
    open.mockRestore()
  })

  it('sends the write grants the person chose and reports a failure', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: '' })
    connect.mockRejectedValue({ response: { data: 'This server has no Google app set up yet.' } })
    const host = await render()
    const choose = async (label: string, value: string) => {
      const select = host.querySelector(`select[aria-label="${label}"]`) as HTMLSelectElement
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, value)
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
    }
    await choose('Gmail access', 'write')
    await choose('Docs access', 'write')
    await act(async () => { ([...host.querySelectorAll('button')].find(b => b.textContent?.includes('Connect Google account')) as HTMLButtonElement).click() })
    await act(async () => { await Promise.resolve() })
    const request = connect.mock.calls[0][0]
    expect(request.allow_agent_write_access).toBe(true)
    expect(request.services).toContainEqual({ service: 'docs', write: true })
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('no Google app')
  })
})
