// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GmailInboundState } from '../../../services/api-types'
import { TooltipProvider } from '../../ui/tooltip'

vi.mock('../../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: { getGmailInboundRoute: vi.fn(), confirmGmailSenderConsent: vi.fn() } }))
vi.mock('../../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn().mockResolvedValue({}) }))
import { agentApi } from '../../../services/api'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import { GmailInboundPanel } from './GmailInboundPanel'
import { useCapabilitiesStore } from '../../../stores/useCapabilitiesStore'
import { getGoogleAppsAskAIMessage } from './gmailAskAI'
import { getIntegrationTabAskAIMessage } from '../workspaceAskAI'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const enabled: GmailInboundState = { configured: true, watch_ready: true, route: { id: 'route', address: 'owner+agent-route@example.com', connection_id: 'gmail', enabled: true, reply: true }, deliveries: [] }
const connections = [{ id: 'gmail', email: 'owner@example.com', display_name: 'Owner', enabled: true, allow_read_access: true, is_default: true, ready: true, auth: { authenticated: true, has_gmail_scope: true, gws_installed: true } }]

describe('Gmail incoming email settings', () => {
  let host: HTMLDivElement
  let root: Root
  const render = async (path = 'Workflow/test', onAsk?: (message: string) => void) => { await act(async () => root.render(<TooltipProvider><GmailInboundPanel workspacePath={path} connections={connections} onAsk={onAsk} /></TooltipProvider>)) }
  beforeEach(() => { useCapabilitiesStore.setState({ capabilities: { providers: [], streaming: true, sse: true, agent_modes: [], tracing: { enabled: false, provider: 'noop' }, workspace: {}, servers: [], local_mode: false } }); vi.resetAllMocks(); vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue(enabled); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); useCapabilitiesStore.setState({ capabilities: null }); vi.restoreAllMocks() })

  const ask = async () => {
    const button = [...host.querySelectorAll('button')].find(b => b.textContent === 'Ask AI')!
    const clock = vi.spyOn(Date, 'now').mockReturnValue(1000)
    await act(async () => button.click())
    clock.mockReturnValue(1700)
    await act(async () => button.click())
  }

  it('hides incoming UI and setup prompts locally without fetching the route, then restores server UI', async () => {
    useCapabilitiesStore.setState({ capabilities: { ...useCapabilitiesStore.getState().capabilities!, local_mode: true } })
    await render()
    expect(host.textContent).toBe('')
    expect(agentApi.getGmailInboundRoute).not.toHaveBeenCalled()
    for (const message of [getGoogleAppsAskAIMessage('Code'), getIntegrationTabAskAIMessage('gmail')]) {
      expect(message).toContain('connect Google apps')
      expect(message).not.toContain('setup_gmail_inbound')
      expect(message).not.toContain('automatic incoming Gmail')
    }
    await act(async () => useCapabilitiesStore.setState({ capabilities: { ...useCapabilitiesStore.getState().capabilities!, local_mode: false } }))
    expect(host.textContent).toContain('Incoming email')
    expect(agentApi.getGmailInboundRoute).toHaveBeenCalledTimes(1)
    expect(getIntegrationTabAskAIMessage('gmail')).toContain('setup_gmail_inbound')
  })

  it('offers setup help when the deployment is disabled and sends it to this workflow Builder', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: false, route: null, deliveries: [] })
    await render()
    expect(host.textContent).toContain('Automatic incoming email is not set up')
    expect(host.textContent).toContain('Google sign-in connects your account')
    expect(host.textContent).toContain('GMAIL_INBOUND_TOPICS')
    expect(host.querySelector('details')?.open).toBe(false)
    await ask()
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledExactlyOnceWith({ workspacePath: 'Workflow/test', message: expect.stringContaining('Inspect get_gmail_trigger') })
    expect(agentApi.getGmailInboundRoute).toHaveBeenCalledTimes(1)
  })

  it('explains a missing client mapping even when the receiver is enabled and shows its public event URL', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: true, route: null, deliveries: [], setup: {
      oauth_clients: [], can_connect_account: false,
      admin_setup: { push_endpoint: 'https://video.realtrainingsys.com/api/hooks/gmail/events', required_access: '', explanation: '', environment_variables: [], steps: [], empty_client_list: '', local_setup: '', documentation_url: '' },
    } })
    await render()
    expect(host.textContent).toContain('Automatic incoming email is not set up')
    expect(host.textContent).toContain('https://video.realtrainingsys.com/api/hooks/gmail/events')
    expect(host.textContent).not.toContain('No Gmail trigger configured')
  })

  it('uses the project chat override rather than looking up a workflow for a Code project', async () => {
    const onAsk = vi.fn()
    await render('Chats/Code/projects/code-1', onAsk)
    await ask()
    expect(onAsk).toHaveBeenCalledExactlyOnceWith(expect.stringContaining('wait for me to complete consent'))
    expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
  })

  it('shows the administrator review and progress without exposing configuration editors', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: false, route: null, deliveries: [], setup: {
      oauth_clients: [], can_connect_account: true,
      provisioning: { available: true, can_prepare: true, oauth_clients: [{ name: 'rts-app', project_id: 'rts-project' }], job: {
        id: 'review', stage: 'Waiting for administrator review and Google consent', expires_at: '2026-10-04T18:00:00Z', review_url: 'https://video.realtrainingsys.com/api/gmail-inbound/setup/start?plan_id=review',
        plan: { client_name: 'rts-app', project_id: 'rts-project', delivery_project_id: 'rts-project', push_endpoint: 'https://video.realtrainingsys.com/api/hooks/gmail/events', topic: 'projects/rts-project/topics/mail', subscription: 'projects/rts-project/subscriptions/mail', push_service_account: 'push@rts-project.iam.gserviceaccount.com' },
      } },
    } })
    const onAsk = vi.fn()
    await render('Chats/Code/projects/code-1', onAsk)
    const section = host.querySelector('[aria-label="Incoming email server setup"]')!
    expect(section.textContent).toContain('Waiting for administrator review')
    expect(section.textContent).toContain('rts-project')
    expect(section.querySelector('a')?.href).toContain('/api/gmail-inbound/setup/start?plan_id=review')
    expect(section.querySelector('input, select, button')).toBeNull()
    await ask()
    expect(onAsk).toHaveBeenCalledWith(expect.stringContaining('setup_gmail_inbound(action="prepare")'))
    expect(onAsk).toHaveBeenCalledWith(expect.stringContaining('never follow it through agent tools'))
  })

  it('reports setup failures independently from mailbox delivery readiness', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, setup: {
      oauth_clients: ['rts-app'], can_connect_account: true,
      provisioning: { available: true, can_prepare: true, oauth_clients: [], job: {
        id: 'review', stage: 'Setup failed', error: 'Google Cloud permissions are missing. Retry setup after fixing access.', expires_at: '2026-10-04T18:00:00Z',
        plan: { client_name: 'rts-app', project_id: 'rts-project', delivery_project_id: 'rts-project', push_endpoint: 'https://video.realtrainingsys.com/api/hooks/gmail/events', topic: '', subscription: '', push_service_account: '' },
      } },
    } })
    await render()
    expect(host.textContent).toContain('Google Cloud permissions are missing')
    expect(host.textContent).toContain('Ready to receive email.')
    expect(host.textContent).toContain('Infrastructure readiness is separate')
    expect(host.querySelector('[aria-label="Incoming email server setup"] a')).toBeNull()
  })

  it('blocks sender activation until an explicit owner acknowledgement and refreshes after consent', async () => {
    const configHash = 'a'.repeat(64)
    const pending: GmailInboundState = { ...enabled, sender_consent: { required: true, approved: false, config_hash: configHash, senders: ['person@gmail.com', '@realtrainingsys.com'] } }
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue(pending)
    vi.mocked(agentApi.confirmGmailSenderConsent).mockResolvedValue({ approved: true })
    await render()
    const approve = [...host.querySelectorAll('button')].find(b => b.textContent === 'Approve additional senders')!
    expect(approve.disabled).toBe(true)
    expect(host.textContent).toContain('connected accounts and files')
    expect(host.textContent).toContain('Additional senders are blocked')
    expect(host.textContent).not.toContain('Ready to receive email.')
    expect(agentApi.confirmGmailSenderConsent).not.toHaveBeenCalled()
    await act(async () => (host.querySelector('input[type="checkbox"]') as HTMLInputElement).click())
    expect(approve.disabled).toBe(false)
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...pending, sender_consent: { ...pending.sender_consent!, approved: true } })
    await act(async () => approve.click())
    expect(agentApi.confirmGmailSenderConsent).toHaveBeenCalledExactlyOnceWith('Workflow/test', configHash, 'approve')
    expect(host.textContent).toContain('Additional sender access approved')
    expect(host.textContent).toContain('Revoke additional sender access')
  })

  it('does not offer approval for a legacy public-domain policy', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, sender_consent: { required: true, approved: false, config_hash: 'b'.repeat(64), senders: ['@gmail.com'], blocked_reason: 'Public mailbox domains are not allowed' } })
    await render()
    expect(host.textContent).toContain('Ask Builder to use exact addresses')
    const approve = [...host.querySelectorAll('button')].find(b => b.textContent === 'Approve additional senders')!
    const checkbox = host.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(approve.disabled).toBe(true)
    expect(checkbox.disabled).toBe(true)
    expect(agentApi.confirmGmailSenderConsent).not.toHaveBeenCalled()
  })

  it('revokes approval through the owner endpoint without modifying the trigger configuration', async () => {
    const configHash = 'c'.repeat(64)
    const approved: GmailInboundState = { ...enabled, sender_consent: { required: true, approved: true, config_hash: configHash, senders: ['person@example.com'] } }
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue(approved)
    vi.mocked(agentApi.confirmGmailSenderConsent).mockResolvedValue({ approved: false })
    await render()
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...approved, sender_consent: { ...approved.sender_consent!, approved: false } })
    const revoke = [...host.querySelectorAll('button')].find(b => b.textContent === 'Revoke additional sender access')!
    await act(async () => revoke.click())
    expect(agentApi.confirmGmailSenderConsent).toHaveBeenCalledExactlyOnceWith('Workflow/test', configHash, 'revoke')
    expect(host.textContent).toContain('Your approval is required')
  })

  it('shows saved routing without controls that can change it', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, workflow_trigger: true, route_selections: { triage: 'support' }, group_names: ['prod'] } })
    await render()
    expect(host.textContent).toContain(enabled.route!.address)
    expect(host.textContent).toContain('triage → support')
    expect(host.textContent).toContain('Groups: prod')
    expect(host.textContent).toContain('Ready to receive email.')
    expect(host.textContent).toContain('Ask Builder')
    expect(host.querySelector('select, input, form')).toBeNull()
    expect([...host.querySelectorAll('button')].map(b => b.textContent)).toEqual(['Ask AI', 'Copy email address', 'Fetch emails'])
  })

  it('fetches Gmail through this target chat without merely refreshing stored activity', async () => {
    const onAsk = vi.fn()
    await render('Chats/Code/projects/code-1', onAsk)
    const fetch = [...host.querySelectorAll('button')].find(b => b.textContent === 'Fetch emails')!
    const clock = vi.spyOn(Date, 'now').mockReturnValue(1000)
    await act(async () => fetch.click())
    clock.mockReturnValue(1700)
    await act(async () => fetch.click())
    expect(onAsk).toHaveBeenCalledExactlyOnceWith(expect.stringContaining('Read recent Gmail messages with google_workspace_cli'))
    expect(onAsk.mock.calls[0][0]).toContain('Mailbox reading does not require Pub/Sub')
    expect(agentApi.getGmailInboundRoute).toHaveBeenCalledTimes(1)
    expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
  })

  it('sends Fetch emails to the workflow Builder when there is no product chat override', async () => {
    await render()
    const fetch = [...host.querySelectorAll('button')].find(b => b.textContent === 'Fetch emails')!
    const clock = vi.spyOn(Date, 'now').mockReturnValue(1000)
    await act(async () => fetch.click())
    clock.mockReturnValue(1700)
    await act(async () => fetch.click())
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledExactlyOnceWith({ workspacePath: 'Workflow/test', message: expect.stringContaining('Fetch recent matching Gmail emails') })
  })

  it('refreshes saved configuration when its refresh token changes', async () => {
    await render()
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, enabled: false } })
    await act(async () => root.render(<TooltipProvider><GmailInboundPanel workspacePath="Workflow/test" connections={connections} refreshToken={1} /></TooltipProvider>))
    expect(host.textContent).toContain('Incoming email is disabled.')
    expect(agentApi.getGmailInboundRoute).toHaveBeenCalledTimes(2)
  })

  it('explains alternative sender and phrase rules without offering setup editors', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, filters: { sender_allowlist: ['@realtrainingsys.com', 'updates@vendor.example'], subject_contains_any: ['Real Training', 'Notion'], allow_automatic: true } } })
    await render()
    expect(host.textContent).toContain('Sender is @realtrainingsys.com OR updates@vendor.example')
    expect(host.textContent).toContain('Subject contains any: “Real Training” OR “Notion”')
    expect(host.textContent).toContain('Listed senders only')
    expect(host.textContent).toContain('Automated notifications from listed senders: Allowed')
    expect(host.textContent).not.toContain('Owner email only')
    expect(host.querySelector('select, input, form')).toBeNull()
  })

  it('shows combined filters read-only, including an explicit no-attachments condition', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, filters: { subject_contains: ['invoice', 'RTS'], body_contains: ['approved'], has_attachments: false, new_threads_only: true } }, deliveries: [{ id: 'skipped', status: 'filtered', session_id: '', error: 'Body does not match the required keywords' }] })
    await render()
    expect(host.textContent).toContain('Subject contains “invoice”')
    expect(host.textContent).toContain('Body contains “approved”')
    expect(host.textContent).toContain('No attachments')
    expect(host.textContent).toContain('New threads only')
    expect(host.textContent).toContain('all condition groups must match')
    expect(host.textContent).toContain('Body does not match the required keywords')
    expect(host.querySelector('select, input, form')).toBeNull()
    expect([...host.querySelectorAll('button')].map(b => b.textContent)).toEqual(['Ask AI', 'Copy email address', 'Fetch emails'])
  })

  it('shows ordered project rules, saved messages, paused state and matched activity read-only', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, rules: [
      { id: 'rts', name: 'Training requests', filters: { sender_allowlist: ['@realtrainingsys.com'], subject_contains_any: ['Real Training', 'RTS'] }, instruction: 'Send X message' },
      { id: 'notion', name: 'Notion updates', enabled: false, filters: { sender_allowlist: ['updates@vendor.example'], allow_automatic: true }, instruction: 'Send Y message' },
    ] }, deliveries: [{ id: 'delivery', status: 'completed', session_id: 'chat', rule_id: 'rts', rule_name: 'Training requests' }] })
    const onAsk = vi.fn()
    await render('Chats/Code/projects/code-1', onAsk)
    const cards = [...host.querySelectorAll('[aria-label="Email rules"] ol > li')]
    expect(cards).toHaveLength(2)
    expect(cards[0].textContent).toContain('1Training requestsEnabled')
    expect(cards[0].textContent).toContain('Send X message')
    expect(cards[0].textContent).toContain('Senders: @realtrainingsys.com')
    expect(cards[1].textContent).toContain('2Notion updatesPaused')
    expect(cards[1].textContent).toContain('Send Y message')
    expect(host.textContent).toContain('first matching enabled rule runs')
    expect(host.textContent).toContain('Training requests · completed')
    expect(host.querySelector('select, input, textarea, form')).toBeNull()
    await ask()
    expect(onAsk.mock.calls[0][0]).toContain('ordered named rules with stable IDs')
    expect(onAsk.mock.calls[0][0]).toContain('preserve untouched rules and their IDs')
  })

  it('shows each workflow binding and inherited senders instead of an apparent full-workflow default', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, workflow_trigger: true, filters: { sender_allowlist: ['@realtrainingsys.com'] }, rules: [
      { id: 'support', name: 'Support', filters: { subject_contains: ['help'] }, route_selections: { triage: 'support' }, group_names: ['prod'] },
      { id: 'billing', name: 'Billing', filters: { subject_contains: ['invoice'] }, route_selections: { triage: 'billing' }, group_names: ['finance'] },
      { id: 'audit', name: 'Audit', step_id: 'audit-step', group_names: ['prod'] },
    ] } })
    await render()
    expect(host.textContent).toContain('Runs: triage → support')
    expect(host.textContent).toContain('Runs: triage → billing')
    expect(host.textContent).toContain('Runs: Step audit-step')
    expect(host.textContent).toContain('Groups: finance')
    expect(host.textContent).toContain('Common filters: Sender is @realtrainingsys.com')
    expect(host.textContent).not.toContain('Starts: Full workflow')
    expect(host.textContent).not.toContain('Message to chat')
    expect(host.querySelectorAll('[aria-label="Email rules"] ol > li')).toHaveLength(3)
    expect(host.querySelector('select, input, textarea, form')).toBeNull()
  })

  it('ignores a late response from the previously selected workspace', async () => {
    let resolveOld!: (value: GmailInboundState) => void
    vi.mocked(agentApi.getGmailInboundRoute).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    await render('Workflow/old')
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: false, route: null, deliveries: [] })
    await render('Workflow/new')
    await act(async () => resolveOld(enabled))
    expect(host.textContent).toContain('Automatic incoming email is not set up')
    expect(host.textContent).toContain('Google sign-in connects your account')
    expect(host.textContent).toContain('GMAIL_INBOUND_TOPICS')
    expect(host.querySelector('details')?.open).toBe(false)
    expect(host.textContent).not.toContain(enabled.route!.address)
  })

  it('surfaces ownership rejection and does not offer controls', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockRejectedValue({ response: { data: 'only the project owner can configure incoming email' } })
    await render()
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('only the project owner')
    expect(host.querySelector('select')).toBeNull()
  })
})
