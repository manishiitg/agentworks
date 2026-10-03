// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { GmailConnection } from '../../services/api-types'
import { GoogleAccountList } from './GoogleAccountList'
import { CHANGE_GOOGLE_ACCESS_EVENT, googleAccessSummary } from './googleAccountAccess'

const account = (extra: Partial<GmailConnection>): GmailConnection => ({
  id: 'c1', display_name: 'Google account', email: 'me@x.com', enabled: true, is_default: false, ready: true,
  allow_read_access: true, services: [{ service: 'drive' }, { service: 'calendar' }], auth: {} as GmailConnection['auth'], ...extra,
})

const cleanups: (() => void)[] = []
afterEach(() => cleanups.splice(0).forEach(fn => fn()))

describe('Google accounts in a Code', () => {
  it('permits only removal when an account owner cannot manage shared settings', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove() })
    const remove = vi.fn()
    const reconnect = vi.fn()
    await act(async () => { root.render(<GoogleAccountList connections={[account({ can_remove: true })]} busyId={null} readOnly canRemove={conn => conn.can_remove === true} onSendTest={vi.fn()} onToggle={vi.fn()} onReconnect={reconnect} onRemove={remove} />) })
    expect([...host.querySelectorAll('button')].find(button => button.textContent === 'Change access')!.disabled).toBe(true)
    await act(async () => { (host.querySelector('[aria-label="More for me@x.com"]') as HTMLButtonElement).click() })
    const items = [...host.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')]
    expect(items.filter(item => !item.disabled).map(item => item.textContent)).toEqual(['Remove'])
    await act(async () => { items.find(item => item.textContent === 'Reconnect')!.click(); items.find(item => item.textContent === 'Remove')!.click() })
    expect(reconnect).not.toHaveBeenCalled()
    expect(remove).toHaveBeenCalledTimes(1)
  })

  it('says what the agent may do in one line', () => {
    expect(googleAccessSummary(account({}))).toBe('Gmail: read · Drive, Calendar: read')
    expect(googleAccessSummary(account({ allow_agent_write_access: true, services: [{ service: 'docs', write: true }] }))).toBe('Gmail: read, draft and send · Docs: read and edit')
  })

  it('shows one row per account and opens the Connect form prefilled for Change access', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove() })
    const seen = vi.fn()
    window.addEventListener(CHANGE_GOOGLE_ACCESS_EVENT, seen)
    cleanups.push(() => window.removeEventListener(CHANGE_GOOGLE_ACCESS_EVENT, seen))
    await act(async () => { root.render(<GoogleAccountList connections={[account({})]} busyId={null} onSendTest={vi.fn()} onToggle={vi.fn()} onReconnect={vi.fn()} onRemove={vi.fn()} />) })
    expect(host.textContent).toContain('me@x.com')
    expect(host.textContent).toContain('Current agent access')
    expect(host.textContent).toContain('Gmail: Read only')
    expect(host.textContent).toContain('Drive: Read only')
    expect(host.textContent).toContain('Docs: No access')
    expect(host.textContent).not.toContain('Raw granted scopes')
    const change = [...host.querySelectorAll('button')].find(button => button.textContent === 'Change access')!
    await act(async () => { change.click() })
    expect(seen).toHaveBeenCalledTimes(1)
    expect((seen.mock.calls[0][0] as CustomEvent).detail).toMatchObject({ email: 'me@x.com', gmail: 'read', levels: { drive: 'read', calendar: 'read' } })
  })
})
