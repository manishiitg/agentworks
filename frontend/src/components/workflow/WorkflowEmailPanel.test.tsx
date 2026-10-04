// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const { status, connect, clients, access, refresh } = vi.hoisted(() => ({ status: vi.fn(), connect: vi.fn(), clients: vi.fn(), access: { readOnly: false }, refresh: vi.fn() }))
vi.mock('../../api/googleApp', () => ({ googleAppApi: { status, connect, clients } }))
vi.mock('./bots/useWorkflowBots', () => ({ useWorkflowBots: () => ({ gmailConnectionsReadOnly: access.readOnly, loadGmailConnections: refresh }) }))
vi.mock('./bots/GmailNotifications', () => ({ GmailNotifications: ({ platformConnect }: { platformConnect?: React.ReactNode }) => <div>{platformConnect || 'Legacy client upload'}</div> }))
vi.mock('./bots/GmailSetupGuide', () => ({ GmailSetupGuide: () => null }))
import WorkflowEmailPanel from './WorkflowEmailPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
beforeEach(() => { vi.clearAllMocks(); access.readOnly = false; status.mockResolvedValue({ configured: true }); clients.mockResolvedValue([]); connect.mockResolvedValue({ id: 'new', auth_url: 'https://accounts.google.com/auth' }) })
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.restoreAllMocks() })

async function render(path: string) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<WorkflowEmailPanel workspacePath={path} />))
  return host
}

it.each(['Chats/Code/projects/app', '_users/alice/Chats/Code/projects/app', 'Chats/Work/projects/crew', 'Workflow/support', 'Workflow/relays/support'])('offers the same Google sign-in flow for %s', async path => {
  vi.spyOn(window, 'open').mockImplementation(() => null)
  const host = await render(path)
  expect(host.querySelector('[data-testid="google-account-connect"]')).not.toBeNull()
  expect(host.textContent).not.toContain('Legacy client upload')
  expect(host.textContent).toContain(path.includes('/Code/') ? 'It stays in this Code' : 'shared by Crews and workflows')
  await act(async () => ([...host.querySelectorAll('button')].find(b => b.textContent === 'Connect Google account') as HTMLButtonElement).click())
  expect(connect).toHaveBeenCalledWith(expect.objectContaining({ workspace_path: path }))
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(refresh).toHaveBeenCalledTimes(1)
})

it('retains shared-account management restrictions in the unified flow', async () => {
  access.readOnly = true
  const host = await render('Workflow/support')
  const button = [...host.querySelectorAll('button')].find(b => b.textContent === 'Connect Google account') as HTMLButtonElement
  expect(button.disabled).toBe(true)
  await act(async () => button.click())
  expect(connect).not.toHaveBeenCalled()
})

it('uses the new form with JSON upload when a deployment has no company app', async () => {
  status.mockResolvedValue({ configured: false })
  const host = await render('Workflow/support')
  expect(host.textContent).not.toContain('Legacy client upload')
  expect(host.querySelector('[data-testid="google-account-connect"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="Google Cloud client file"]')).not.toBeNull()
})
