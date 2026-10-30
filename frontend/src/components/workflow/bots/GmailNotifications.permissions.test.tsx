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
vi.mock('../../integrations/PersonalAccountsPanel', () => ({ default: () => <div>Saved account picker</div> }))
vi.mock('../../../utils/projectIntegrationNavigation', () => ({ openProjectIntegrations: vi.fn() }))
vi.mock('./GmailSetupGuide', () => ({ GmailSetupGuide: () => null }))
vi.mock('../../../api/googleApp', () => ({ googleAppApi: { status: vi.fn(async () => ({ configured: false })), clients: vi.fn(async () => [{ name: 'test-client' }]), connectWithClient: vi.fn(), registerClient: vi.fn(), reconnect: vi.fn() } }))
vi.mock('../../../services/api', () => ({
  agentApi: {

    listSavedGoogleAccounts: vi.fn(async () => ({ connections: [] })),
    getBotConfig: vi.fn(async () => ({ allowed_emails: [] })),
    getGmailFeedbackConfig: vi.fn(async () => ({
      enabled: true, default_to: 'alice@example.com', blocked_recipients: [], ready: true,
      auth: { authenticated: true, has_gmail_scope: true, gws_installed: true },
    })),
    listGmailConnections: vi.fn(async (workspace?: string) => ({ can_add: access.canWrite, connections: [{
      id: 'gmail_002', display_name: 'Test mailbox', client_name: 'test-client', can_manage: access.user.is_admin || !!workspace?.includes('/Code/'), can_remove: access.user.is_admin,
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
import { googleAppApi } from '../../../api/googleApp'
import { openProjectIntegrations } from '../../../utils/projectIntegrationNavigation'
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

  it('shows the same saved-account picker while company delivery stays admin-managed', async () => {
    await render()
    expect(host.textContent).toContain('Saved account picker')
    expect(host.querySelector('[data-testid="google-account-connect"]')).toBeNull()
    expect(button('Save').disabled).toBe(true)
    expect(host.textContent).toContain('Workflow email notifications')
  })

  it('permits removal of a server-identified own account without other shared management', async () => {
    vi.mocked(agentApi.listGmailConnections).mockResolvedValueOnce({ can_add: true, connections: [{
      id: 'gmail_002', display_name: 'Own mailbox', client_name: 'test-client', can_remove: true, can_manage: false,
      enabled: true, is_default: false, ready: true, auth: { authenticated: true, has_gmail_scope: true, gws_installed: true },
    }] })
    await render(); await openMenu()
    expect(button('Remove').disabled).toBe(false)
    for (const label of ['Change access', 'Reconnect', 'Make default', 'Send a test email', 'Turn off', 'Save']) expect(button(label).disabled, label).toBe(true)
    await act(async () => button('Remove').click())
    expect(agentApi.deleteGmailConnection).toHaveBeenCalledWith('gmail_002')
    expect(agentApi.deleteGmailOAuthClient).not.toHaveBeenCalled()
  })

  it('starts sign-in for the existing company account instead of navigating to the same panel', async () => {
    access.user.is_admin = true
    vi.mocked(agentApi.listGmailConnections).mockResolvedValueOnce({ can_add: true, connections: [{
      id: 'gmail_002', display_name: 'Test mailbox', email: 'owner@example.com', client_name: 'test-client', can_manage: true, can_reconnect: true,
      enabled: true, is_default: false, auth: { authenticated: false, has_gmail_scope: true, gws_installed: true }, ready: false, allow_read_access: false, allow_agent_write_access: false, services: [{ service: 'docs', write: true }],
    }] })
    const popup = { opener: {}, closed: false, location: { href: '' }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.mocked(googleAppApi.reconnect).mockResolvedValueOnce({ id: 'gmail_002', auth_url: 'https://accounts.google.com/auth' })
    await render()
    await act(async () => button('Sign in again').click())
    expect(open).toHaveBeenCalledWith('about:blank', '_blank')
    expect(open.mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(googleAppApi.reconnect).mock.invocationCallOrder[0])
    expect(googleAppApi.reconnect).toHaveBeenCalledWith('gmail_002', { workspace_path: 'Workflow/test', services: [{ service: 'docs', write: true }], allow_read_access: false, allow_agent_write_access: false })
    expect(popup.opener).toBeNull()
    expect(popup.location.href).toBe('https://accounts.google.com/auth')
    expect(openProjectIntegrations).not.toHaveBeenCalled()
    expect(host.querySelector('a[href="https://accounts.google.com/auth"]')).not.toBeNull()
  })

  it('opens company access changes in the same workspace without granting new access', async () => {
    access.user.is_admin = true
    await render()
    expect(host.querySelector('[data-testid="google-account-connect"]')).toBeNull()
    await act(async () => button('Change access').click())
    expect(host.querySelector('[data-testid="google-account-connect"]')).not.toBeNull()
    expect(host.querySelector<HTMLSelectElement>('[aria-label="Gmail access"]')!.value).toBe('off')
    expect(openProjectIntegrations).not.toHaveBeenCalled()
    expect(googleAppApi.reconnect).not.toHaveBeenCalled()
  })

  it('allows an admin to remove an unused company client', async () => {
    access.user.is_admin = true
    await render(); await openMenu()
    await act(async () => button('Remove').click())
    expect(agentApi.deleteGmailConnection).toHaveBeenCalledWith('gmail_002')
    expect(agentApi.deleteGmailOAuthClient).toHaveBeenCalledWith('test-client')
  })

  it('shows company management from the server and respects Builder access', async () => {
    Object.assign(access, { isMultiUserMode: false, isMultiUserModeChecked: false })
    await render(); expect(button('Change access').disabled).toBe(true)
    access.user.is_admin = true
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
