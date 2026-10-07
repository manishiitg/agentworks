// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { status, connect, reconnect, clients, registerClient, connectWithClient } = vi.hoisted(() => ({ status: vi.fn(), connect: vi.fn(), reconnect: vi.fn(), clients: vi.fn(), registerClient: vi.fn(), connectWithClient: vi.fn() }))
vi.mock('../../api/googleApp', () => ({ googleAppApi: { status, connect, reconnect, clients, registerClient, connectWithClient } }))

vi.mock('../../components/workflow/bots/GmailSetupGuide', () => ({ GmailSetupGuide: () => null }))

import { GoogleAccountConnect } from './GoogleAccountConnect'
import { changeGoogleAccountAccess } from './googleAccountAccess'
import type { GmailConnection } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
beforeEach(() => clients.mockResolvedValue([]))
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks(); vi.restoreAllMocks(); document.body.innerHTML = '' })

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
  it('opens access changes and lets the person collapse and reopen without losing edits', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: '' })
    const host = await render()
    const toggle = host.querySelector<HTMLButtonElement>('button[aria-controls]')!
    await act(async () => toggle.click())
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    await act(async () => changeGoogleAccountAccess({ id: 'existing', email: 'me@example.com', allow_read_access: true } as GmailConnection, 'Chats/Code/projects/p1'))
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    await act(async () => (host.querySelector('[aria-label="Add Docs access"]') as HTMLButtonElement).click())
    await act(async () => toggle.click())
    const content = host.querySelector<HTMLElement>(`[id="${toggle.getAttribute('aria-controls')}"]`)!
    expect(content.hidden).toBe(true)
    expect(toggle.textContent).toContain('1 unsaved change')
    await act(async () => toggle.click())
    expect(content.hidden).toBe(false)
    expect((host.querySelector('[aria-label="Docs access"]') as HTMLSelectElement).value).toBe('read')
    expect(connect).not.toHaveBeenCalled()
    expect(reconnect).not.toHaveBeenCalled()
  })

  it('keeps saved access visible while adding and removing services, and discards edits on Cancel', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: '' })
    const host = await render()
    const account = { id: 'existing', email: 'me@example.com', allow_read_access: true, services: [{ service: 'drive', write: true }] } as GmailConnection
    await act(async () => changeGoogleAccountAccess(account, 'Chats/Code/projects/p1'))
    const drive = host.querySelector('[data-testid="google-access-drive"]')!
    const docs = host.querySelector('[data-testid="google-access-docs"]')!
    expect(drive.textContent).toContain('Current: Read and edit')
    expect(docs.textContent).toContain('Current: No access')
    await act(async () => (host.querySelector('[aria-label="Remove Drive access"]') as HTMLButtonElement).click())
    await act(async () => (host.querySelector('[aria-label="Add Docs access"]') as HTMLButtonElement).click())
    expect((drive.querySelector('select') as HTMLSelectElement).value).toBe('off')
    expect((docs.querySelector('select') as HTMLSelectElement).value).toBe('read')
    expect(drive.textContent).toContain('Current: Read and edit')
    expect(docs.textContent).toContain('Current: No access')
    expect(host.textContent).toContain('2 unsaved changes')
    expect(connect).not.toHaveBeenCalled()
    expect(reconnect).not.toHaveBeenCalled()
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Cancel')!.click())
    expect(host.textContent).not.toContain('Current:')
    expect((drive.querySelector('select') as HTMLSelectElement).value).toBe('read')
    expect((docs.querySelector('select') as HTMLSelectElement).value).toBe('off')
  })

  it('removes Gmail agent access without removing its notification permission', async () => {
    status.mockResolvedValue({ configured: true, redirect_uri: '' })
    connect.mockResolvedValue({ id: 'c1', auth_url: 'https://accounts.google.com/auth' })
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    try {
      const host = await render()
      await act(async () => (host.querySelector('[aria-label="Remove Gmail access"]') as HTMLButtonElement).click())
      expect(host.querySelector('[data-testid="google-access-gmail"]')?.textContent).toContain('Notifications only')
      await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Connect Google account'))!.click())
      expect(connect).toHaveBeenCalledWith(expect.objectContaining({ allow_read_access: false, allow_agent_write_access: false }))
    } finally { open.mockRestore() }
  })

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
    expect(host.textContent).toContain('only an administrator can connect one')
    expect([...host.querySelectorAll('select')].every(select => select.disabled)).toBe(true)
    expect([...host.querySelectorAll<HTMLButtonElement>('button[aria-label$=" access"]')].every(button => button.disabled)).toBe(true)
    const locked = [...host.querySelectorAll('button')].find(b => b.textContent === 'Only an admin can connect') as HTMLButtonElement
    expect(locked.disabled).toBe(true)
    await act(async () => locked.click())
    await act(async () => changeGoogleAccountAccess({ id: 'shared', email: 'me@example.com' } as GmailConnection, 'Workflow/support'))
    expect(host.textContent).not.toContain('Change access for')
    expect(connect).not.toHaveBeenCalled()
    expect(reconnect).not.toHaveBeenCalled()
  })

  it('offers the new permission form and JSON upload without a company app', async () => {
    status.mockResolvedValue({ configured: false, redirect_uri: '' })
    const host = await render()
    expect(host.querySelector('[data-testid="google-account-connect"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Google Cloud client file"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Gmail access"]')).not.toBeNull()
  })

  it('reconnects a legacy account with no company app and preserves its choices', async () => {
    status.mockResolvedValue({ configured: false })
    reconnect.mockResolvedValue({ id: 'old', auth_url: 'https://accounts.google.com/auth' })
    vi.spyOn(window, 'open').mockImplementation(() => null)
    const host = await render({ workspacePath: 'Workflow/local', privateAccount: false })
    await act(async () => changeGoogleAccountAccess({ id: 'old', email: 'me@example.com', allow_read_access: false, services: [{ service: 'docs', write: true }] } as GmailConnection, 'Workflow/local'))
    expect(host.querySelector('[aria-label="Google sign-in app"]')).toBeNull()
    await act(async () => [...host.querySelectorAll('button')].find(b => b.textContent === 'Sign in again with Google')!.click())
    expect(reconnect).toHaveBeenCalledWith('old', expect.objectContaining({ allow_read_access: false, services: [{ service: 'docs', write: true }] }))
    expect(connectWithClient).not.toHaveBeenCalled()
  })

  it('allows company and saved apps together, and uses the selected named client', async () => {
    status.mockResolvedValue({ configured: true })
    clients.mockResolvedValue([{ name: 'local-app' }, { name: 'platform' }])
    connectWithClient.mockResolvedValue({ id: 'new', auth_url: 'https://accounts.google.com/auth' })
    vi.spyOn(window, 'open').mockImplementation(() => null)
    const host = await render()
    const select = host.querySelector<HTMLSelectElement>('[aria-label="Google sign-in app"]')!
    expect(select.value).toBe('company')
    expect([...select.options].map(o => o.value)).toEqual(['company', 'client:local-app', 'upload'])
    await act(async () => { select.value = 'client:local-app'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => [...host.querySelectorAll('button')].find(b => b.textContent === 'Connect Google account')!.click())
    expect(connectWithClient).toHaveBeenCalledWith('local-app', expect.objectContaining({ workspace_path: 'Chats/Code/projects/p1', allow_read_access: true }))
    expect(registerClient).not.toHaveBeenCalled()
    expect(connect).not.toHaveBeenCalled()
  })

  it('uploads a separately named JSON without replacing an existing client', async () => {
    status.mockResolvedValue({ configured: false })
    registerClient.mockResolvedValue({ name: 'my-local-app' })
    connectWithClient.mockResolvedValue({ id: 'new', auth_url: 'https://accounts.google.com/auth' })
    vi.spyOn(window, 'open').mockImplementation(() => null)
    const host = await render({ privateAccount: false, workspacePath: 'Workflow/local' })
    const name = host.querySelector<HTMLInputElement>('[aria-label="Google app name"]')!
    const file = host.querySelector<HTMLInputElement>('[aria-label="Google Cloud client file"]')!
    const json = { web: { client_id: 'test-id', client_secret: 'test-secret' } }
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'my-local-app'); name.dispatchEvent(new Event('input', { bubbles: true }))
      Object.defineProperty(file, 'files', { value: [{ text: async () => JSON.stringify(json) }], configurable: true })
      file.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => [...host.querySelectorAll('button')].find(b => b.textContent === 'Connect Google account')!.click())
    expect(registerClient).toHaveBeenCalledWith('my-local-app', json)
    expect(connectWithClient).toHaveBeenCalledWith('my-local-app', expect.objectContaining({ workspace_path: 'Workflow/local' }))
    expect(host.querySelector('[aria-label="Google Cloud client file"]')).toBeNull()
  })

  it('reports failed app discovery and still permits existing-account reauthorization', async () => {
    status.mockRejectedValue(new Error('offline'))
    clients.mockResolvedValue([])
    const host = await render()
    expect(host.textContent).toContain('Could not check all Google sign-in apps')
    await act(async () => changeGoogleAccountAccess({ id: 'old', email: 'me@example.com', allow_read_access: true } as GmailConnection, 'Chats/Code/projects/p1'))
    expect([...host.querySelectorAll('button')].find(b => b.textContent === 'Sign in again with Google')!.disabled).toBe(false)
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
