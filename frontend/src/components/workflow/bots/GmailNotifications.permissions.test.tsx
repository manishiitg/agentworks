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
vi.mock('../../../api/googleApp', () => ({ googleAppApi: { status: vi.fn(async () => ({ configured: false })), clients: vi.fn(async () => [{ name: 'test-client' }]), connectWithClient: vi.fn(), registerClient: vi.fn(), reconnect: vi.fn() } }))
vi.mock('../../../services/api', () => ({
  agentApi: {

    getGmailInboundRoute: vi.fn(async () => ({ configured: false, route: null, deliveries: [] })),
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
    getApiBaseUrl: () => 'http://localhost:18743',
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

describe('Gmail management permissions with either OAuth source', () => {
  let host: HTMLDivElement
  let root: Root
  const button = (text: string) => [...host.querySelectorAll('button')].find(entry => entry.textContent === text)!
  const render = async (path = 'Workflow/test') => {
    await act(async () => root.render(<Probe workspacePath={path} />))
  }
  const openMenu = async () => {
    await act(async () => (host.querySelector('[aria-label^="More for"]') as HTMLButtonElement).click())
  }

  beforeEach(() => {
    vi.clearAllMocks()
    Object.assign(access, { user: { id: 'alice', is_admin: false }, isMultiUserMode: true, isMultiUserModeChecked: true, canWrite: true })
    vi.stubGlobal('confirm', vi.fn(() => true))
    host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  })
  afterEach(async () => {
    await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks()
  })

  it('keeps the new UI while preventing workflow editors from managing shared accounts', async () => {
    await render()
    expect(host.querySelector('[data-testid="google-account-list"]')).not.toBeNull()
    expect(button('Change access').disabled).toBe(true)
    expect(button('Only an admin can connect').disabled).toBe(true)
    expect(button('Save').disabled).toBe(true)
    expect((host.querySelector('[aria-label="Google sign-in app"]') as HTMLSelectElement).disabled).toBe(true)
    expect(host.textContent).toContain('An admin manages shared Gmail accounts')
    expect(host.textContent).toContain('Workflow email notifications')
    expect(host.textContent).not.toContain('Enable Gmail')
    await act(async () => button('Only an admin can connect').click())
    expect(agentApi.createGmailOAuthClient).not.toHaveBeenCalled()
  })

  it('permits removal of a server-identified own account without other shared management', async () => {
    vi.mocked(agentApi.listGmailConnections).mockResolvedValueOnce({ connections: [{
      id: 'gmail_002', display_name: 'Own mailbox', client_name: 'test-client', can_remove: true,
      enabled: true, is_default: false, ready: true, auth: { authenticated: true, has_gmail_scope: true, gws_installed: true },
    }] })
    await render(); await openMenu()
    expect(button('Remove').disabled).toBe(false)
    for (const label of ['Change access', 'Only an admin can connect', 'Reconnect', 'Make default', 'Send a test email', 'Turn off', 'Save']) expect(button(label).disabled, label).toBe(true)
    await act(async () => button('Remove').click())
    expect(agentApi.deleteGmailConnection).toHaveBeenCalledWith('gmail_002')
    expect(agentApi.deleteGmailOAuthClient).not.toHaveBeenCalled()
  })

  it('allows an admin to remove an unused client and open the JSON upload', async () => {
    access.user.is_admin = true
    await render(); await openMenu()
    await act(async () => button('Remove').click())
    expect(agentApi.deleteGmailConnection).toHaveBeenCalledWith('gmail_002')
    expect(agentApi.deleteGmailOAuthClient).toHaveBeenCalledWith('test-client')
    const select = host.querySelector<HTMLSelectElement>('[aria-label="Google sign-in app"]')!
    await act(async () => { select.value = 'upload'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect((host.querySelector('[aria-label="Google Cloud client file"]') as HTMLInputElement).disabled).toBe(false)
  })

  it('lets Code owners manage private accounts while delivery remains admin-only', async () => {
    await render('Chats/Code/projects/app-1')
    expect(button('Change access').disabled).toBe(false)
    expect(button('Connect Google account').disabled).toBe(false)
    expect(button('Send test email').disabled).toBe(true)
    await render('_users/alice/Chats/Code/projects/app-1')
    expect(button('Change access').disabled).toBe(false)
    await render('_users/bob/Chats/Code/projects/app-1')
    expect(button('Change access').disabled).toBe(true)
    expect(button('Only the owner can connect').disabled).toBe(true)
  })

  it('fails closed until auth mode is known and allows the local installation owner', async () => {
    Object.assign(access, { isMultiUserMode: false, isMultiUserModeChecked: false })
    await render(); expect(button('Change access').disabled).toBe(true)
    access.isMultiUserModeChecked = true
    await render(); expect(button('Change access').disabled).toBe(false)
    access.canWrite = false
    await render(); expect(button('Change access').disabled).toBe(true)
  })

  it('shows the permission reason when access changes after the panel loads', async () => {
    access.user.is_admin = true
    vi.mocked(agentApi.deleteGmailConnection).mockRejectedValueOnce(Object.assign(new Error('Request failed with status code 403'), {
      response: { status: 403, data: { error: 'workflow permission denied', required_access: 'admin' } },
    }))
    await render(); await openMenu(); await act(async () => button('Remove').click())
    expect(host.textContent).toContain('Only an admin can manage shared Gmail accounts and delivery settings.')
    expect(agentApi.deleteGmailOAuthClient).not.toHaveBeenCalled()
  })
})
