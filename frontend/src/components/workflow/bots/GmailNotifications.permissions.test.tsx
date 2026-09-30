// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const access = vi.hoisted(() => ({
  user: { id: 'alice', is_admin: false },
  isMultiUserMode: true,
  isMultiUserModeChecked: true,
  canWrite: true,
}))

vi.mock('../../../stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: typeof access) => unknown) => selector(access),
}))
vi.mock('../../../hooks/useCanWriteWorkflow', () => ({
  useCanWriteWorkflow: () => access.canWrite,
  READ_ONLY_TITLE: 'Read-only access',
}))
vi.mock('../../../stores/useWorkflowManifestStore', () => ({
  useWorkflowManifestStore: (selector: (state: unknown) => unknown) => selector({ workflows: [], refreshWorkflows: vi.fn(), updateWorkflow: vi.fn() }),
}))
vi.mock('../AskAIButton', () => ({ AskAIButton: () => null }))
vi.mock('./GmailSetupGuide', () => ({ GmailSetupGuide: () => null }))
vi.mock('../../../services/api', () => ({
  agentApi: {
    getBotConfig: vi.fn(async () => ({ allowed_emails: [] })),
    getGmailFeedbackConfig: vi.fn(async () => ({
      enabled: true, default_to: 'alice@example.com', blocked_recipients: [], ready: true,
      auth: { authenticated: true, has_gmail_scope: true, gws_installed: true },
    })),
    listGmailConnections: vi.fn(async () => ({ connections: [{
      id: 'gmail_002', display_name: 'Test mailbox', client_name: 'test-client',
      enabled: true, ready: true, auth: { authenticated: true, has_gmail_scope: true },
    }] })),
    listGmailOAuthClients: vi.fn(async () => ({ clients: [] })),
    getGoogleServiceCatalog: vi.fn(async () => ({})),
    deleteGmailConnection: vi.fn(async () => {}),
    deleteGmailOAuthClient: vi.fn(async () => {}),
    createGmailOAuthClient: vi.fn(async () => ({ name: 'new-client' })),
    createGmailConnection: vi.fn(async () => ({ id: 'new-connection' })),
  },
}))

import { agentApi } from '../../../services/api'
import { GmailNotifications } from './GmailNotifications'
import { useWorkflowBots, type WorkflowBots } from './useWorkflowBots'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let latest: WorkflowBots
function Probe({ workspacePath }: { workspacePath: string }) {
  latest = useWorkflowBots(workspacePath, undefined, 'email')
  return <GmailNotifications bots={latest} workspacePath={workspacePath} />
}

describe('Gmail management permissions', () => {
  let host: HTMLDivElement
  let root: Root
  const button = (text: string) => [...host.querySelectorAll('button')].find(entry => entry.textContent === text)!
  const render = async (path = 'Workflow/test') => {
    await act(async () => root.render(<Probe workspacePath={path} />))
  }

  beforeEach(() => {
    vi.clearAllMocks()
    Object.assign(access, { user: { id: 'alice', is_admin: false }, isMultiUserMode: true, isMultiUserModeChecked: true, canWrite: true })
    vi.stubGlobal('confirm', vi.fn(() => true))
    host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    host.remove()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('explains admin access and prevents a workflow editor from adding or removing shared accounts', async () => {
    await render()
    expect(host.textContent).toContain('Only an admin can add, reconnect, change or remove shared Gmail accounts.')
    for (const label of ['+ Add account', 'Remove', 'Reconnect', 'Make default', 'Send test', 'Enable', 'Edit access', 'Save', 'Send test email']) {
      // This mailbox is enabled, so its toggle is labelled Disable.
      const control = button(label === 'Enable' ? 'Disable' : label)
      expect(control.disabled, label).toBe(true)
    }
    await act(async () => { button('Remove').click(); button('+ Add account').click() })
    expect(agentApi.deleteGmailConnection).not.toHaveBeenCalled()
    expect(agentApi.createGmailOAuthClient).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Google Cloud client file"]')).toBeNull()
  })

  it('allows an admin to remove and open the add-account form', async () => {
    access.user.is_admin = true
    await render()
    expect(button('Remove').disabled).toBe(false)
    await act(async () => button('Remove').click())
    expect(agentApi.deleteGmailConnection).toHaveBeenCalledWith('gmail_002')
    await act(async () => button('+ Add account').click())
    expect((host.querySelector('[aria-label="Google Cloud client file"]') as HTMLInputElement).disabled).toBe(false)
  })

  it('lets a Code owner manage private accounts while shared delivery settings remain admin-only', async () => {
    await render('Chats/Code/projects/app-1')
    expect(button('Remove').disabled).toBe(false)
    expect(button('+ Add account').disabled).toBe(false)
    expect(button('Send test email').disabled).toBe(true)
    expect(host.textContent).toContain('Only an admin can change shared Gmail delivery settings')
    await render('_users/alice/Chats/Code/projects/app-1')
    expect(button('Remove').disabled).toBe(false)
    await render('_users/bob/Chats/Code/projects/app-1')
    expect(button('Remove').disabled).toBe(true)
    expect(button('+ Add account').disabled).toBe(true)
  })

  it('fails closed until authentication mode is known and permits the local installation owner', async () => {
    Object.assign(access, { isMultiUserMode: false, isMultiUserModeChecked: false })
    await render()
    expect(button('Remove').disabled).toBe(true)
    access.isMultiUserModeChecked = true
    await render()
    expect(button('Remove').disabled).toBe(false)
    access.canWrite = false
    await render()
    expect(button('Remove').disabled).toBe(true)
  })

  it('shows the server permission reason if admin access changes after the panel loads', async () => {
    access.user.is_admin = true
    vi.mocked(agentApi.deleteGmailConnection).mockRejectedValueOnce(Object.assign(new Error('Request failed with status code 403'), {
      response: { status: 403, data: { error: 'workflow permission denied', required_access: 'admin' } },
    }))
    await render()
    await act(async () => button('Remove').click())
    expect(host.textContent).toContain('Only an admin can manage shared Gmail accounts and delivery settings.')
    expect(host.textContent).not.toContain('Request failed with status code 403')
    expect(agentApi.deleteGmailOAuthClient).not.toHaveBeenCalled()
  })
})
