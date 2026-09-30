// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { status, connect } = vi.hoisted(() => ({ status: vi.fn(), connect: vi.fn() }))
vi.mock('../../api/googleApp', () => ({ googleAppApi: { status, connect } }))

import { GoogleAccountConnect } from './GoogleAccountConnect'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks(); document.body.innerHTML = '' })

const render = async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => act(() => root.unmount()))
  await act(async () => { root.render(<GoogleAccountConnect workspacePath="Chats/Code/projects/p1" />) })
  await act(async () => { await Promise.resolve() })
  return host
}

describe('GoogleAccountConnect', () => {
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
