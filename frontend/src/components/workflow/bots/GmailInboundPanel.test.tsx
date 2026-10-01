// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GmailInboundState } from '../../../services/api-types'

vi.mock('../../../services/api', () => ({ agentApi: { getGmailInboundRoute: vi.fn() } }))
import { agentApi } from '../../../services/api'
import { GmailInboundPanel } from './GmailInboundPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const enabled: GmailInboundState = { configured: true, watch_ready: true, route: { id: 'route', address: 'owner+agent-route@example.com', connection_id: 'gmail', enabled: true, reply: true }, deliveries: [] }
const connections = [{ id: 'gmail', email: 'owner@example.com', display_name: 'Owner', enabled: true, allow_read_access: true, is_default: true, ready: true, auth: { authenticated: true, has_gmail_scope: true, gws_installed: true } }]

describe('Gmail incoming email settings', () => {
  let host: HTMLDivElement
  let root: Root
  const render = async (path = 'Workflow/test') => { await act(async () => root.render(<GmailInboundPanel workspacePath={path} connections={connections} />)) }
  beforeEach(() => { vi.resetAllMocks(); vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue(enabled); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(async () => { await act(async () => root.unmount()); host.remove() })

  it('shows saved routing without controls that can change it', async () => {
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, workflow_trigger: true, route_selections: { triage: 'support' }, group_names: ['prod'] } })
    await render()
    expect(host.textContent).toContain(enabled.route!.address)
    expect(host.textContent).toContain('triage → support')
    expect(host.textContent).toContain('Groups: prod')
    expect(host.textContent).toContain('Ready to receive email.')
    expect(host.textContent).toContain('Ask Builder')
    expect(host.querySelector('select, input, form')).toBeNull()
    expect([...host.querySelectorAll('button')].map(b => b.textContent)).toEqual(['Copy email address', 'Refresh email activity'])
  })

  it('refreshes saved configuration after Builder changes it', async () => {
    await render()
    vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ ...enabled, route: { ...enabled.route!, enabled: false } })
    const refresh = [...host.querySelectorAll('button')].find(b => b.textContent === 'Refresh email activity')!
    await act(async () => refresh.click())
    expect(host.textContent).toContain('Incoming email is disabled.')
    expect(agentApi.getGmailInboundRoute).toHaveBeenCalledTimes(2)
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
