// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GmailInboundState } from '../../../services/api-types'
import { TooltipProvider } from '../../ui/tooltip'

vi.mock('../../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: { getGmailInboundRoute: vi.fn() } }))
vi.mock('../../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn().mockResolvedValue({}) }))
import { agentApi } from '../../../services/api'
import { sendWorkspacePaneMessageToChat } from '../../../utils/workspacePaneChat'
import { GmailInboundPanel } from './GmailInboundPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const enabled: GmailInboundState = { configured: true, watch_ready: true, route: { id: 'route', address: 'owner+agent-route@example.com', connection_id: 'gmail', enabled: true, reply: true }, deliveries: [] }
const connections = [{ id: 'gmail', email: 'owner@example.com', display_name: 'Owner', enabled: true, allow_read_access: true, is_default: true, ready: true, auth: { authenticated: true, has_gmail_scope: true, gws_installed: true } }]

describe('Gmail incoming email settings', () => {
  let host: HTMLDivElement
  let root: Root
  const render = async (path = 'Workflow/test', onAsk?: (message: string) => void) => { await act(async () => root.render(<TooltipProvider><GmailInboundPanel workspacePath={path} connections={connections} onAsk={onAsk} /></TooltipProvider>)) }
  beforeEach(() => { vi.resetAllMocks(); vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue(enabled); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks() })

  const ask = async () => {
    const button = [...host.querySelectorAll('button')].find(b => b.textContent === 'Ask AI')!
    const clock = vi.spyOn(Date, 'now').mockReturnValue(1000)
    await act(async () => button.click())
    clock.mockReturnValue(1700)
    await act(async () => button.click())
  }

  it('offers setup help when the deployment is disabled and sends it to this workflow Builder', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: false, route: null, deliveries: [] })
    await render()
    expect(host.textContent).toContain('An administrator needs to enable')
    await ask()
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledExactlyOnceWith({ workspacePath: 'Workflow/test', message: expect.stringContaining('Inspect get_gmail_trigger') })
    expect(agentApi.getGmailInboundRoute).toHaveBeenCalledTimes(1)
  })

  it('uses the project chat override rather than looking up a workflow for a Code project', async () => {
    const onAsk = vi.fn()
    await render('Chats/Code/projects/code-1', onAsk)
    await ask()
    expect(onAsk).toHaveBeenCalledExactlyOnceWith(expect.stringContaining('wait for me to complete consent'))
    expect(sendWorkspacePaneMessageToChat).not.toHaveBeenCalled()
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

  it('ignores a late response from the previously selected workspace', async () => {
    let resolveOld!: (value: GmailInboundState) => void
    vi.mocked(agentApi.getGmailInboundRoute).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    await render('Workflow/old')
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: false, route: null, deliveries: [] })
    await render('Workflow/new')
    await act(async () => resolveOld(enabled))
    expect(host.textContent).toContain('An administrator needs to enable')
    expect(host.textContent).not.toContain(enabled.route!.address)
  })

  it('surfaces ownership rejection and does not offer controls', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockRejectedValue({ response: { data: 'only the project owner can configure incoming email' } })
    await render()
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('only the project owner')
    expect(host.querySelector('select')).toBeNull()
  })
})
