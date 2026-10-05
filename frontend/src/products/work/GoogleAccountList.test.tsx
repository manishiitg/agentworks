// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { GmailConnection } from '../../services/api-types'
import { GoogleAccountList } from './GoogleAccountList'
import { CHANGE_GOOGLE_ACCESS_EVENT, googleAccessSummary, googleGrantedAccess } from './googleAccountAccess'

const account = (extra: Partial<GmailConnection>): GmailConnection => ({
  id: 'c1', display_name: 'Google account', email: 'me@x.com', enabled: true, is_default: false, ready: true,
  allow_read_access: true, services: [{ service: 'drive' }, { service: 'calendar' }], auth: {} as GmailConnection['auth'], ...extra,
})

const cleanups: (() => void)[] = []
afterEach(() => cleanups.splice(0).forEach(fn => fn()))

describe('Google accounts across products', () => {
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

  it('keeps accounts compact, opens only one permission comparison, and prefills Change access', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    cleanups.push(() => { act(() => root.unmount()); host.remove() })
    const seen = vi.fn()
    window.addEventListener(CHANGE_GOOGLE_ACCESS_EVENT, seen)
    cleanups.push(() => window.removeEventListener(CHANGE_GOOGLE_ACCESS_EVENT, seen))
    await act(async () => { root.render(<GoogleAccountList connections={[account({}), account({ id: 'c2', email: 'other@x.com', services: [] })]} busyId={null} onSendTest={vi.fn()} onToggle={vi.fn()} onReconnect={vi.fn()} onRemove={vi.fn()} />) })
    expect(host.textContent).toContain('me@x.com')
    expect(host.querySelectorAll('table')).toHaveLength(0)
    expect(host.textContent).toContain('Gmail: Read only')
    expect(host.textContent).toContain('Drive: Read only')
    expect(host.textContent).not.toContain('No access')
    await act(async () => (host.querySelector('[aria-label="Show permissions for me@x.com"]') as HTMLButtonElement).click())
    expect(host.querySelectorAll('table')).toHaveLength(1)
    expect(host.querySelector('table')!.textContent).toContain('Google granted')
    expect(host.querySelector('table')!.textContent).toContain('No access')
    await act(async () => (host.querySelector('[aria-label="Show permissions for other@x.com"]') as HTMLButtonElement).click())
    expect(host.querySelectorAll('table')).toHaveLength(1)
    expect(host.querySelector('caption')!.textContent).toBe('Permissions for other@x.com')
    expect(host.querySelector('[aria-label="Show permissions for me@x.com"]')!.getAttribute('aria-expanded')).toBe('false')
    await act(async () => (host.querySelector('[aria-label="Hide permissions for other@x.com"]') as HTMLButtonElement).click())
    expect(host.querySelectorAll('table')).toHaveLength(0)
    expect(host.textContent).not.toContain('Raw granted scopes')
    const change = [...host.querySelectorAll('button')].find(button => button.textContent === 'Change access')!
    await act(async () => { change.click() })
    expect(seen).toHaveBeenCalledTimes(1)
    expect((seen.mock.calls[0][0] as CustomEvent).detail).toMatchObject({ email: 'me@x.com', gmail: 'read', levels: { drive: 'read', calendar: 'read' } })
  })
})

it('distinguishes Google-granted Gmail reading from disabled AgentWorks reading', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<GoogleAccountList connections={[account({ allow_read_access: false, services: [], auth: { scopes: ['https://www.googleapis.com/auth/gmail.readonly', 'https://www.googleapis.com/auth/gmail.send'] } as GmailConnection['auth'] })]} busyId={null} onSendTest={vi.fn()} onToggle={vi.fn()} onReconnect={vi.fn()} onRemove={vi.fn()} />))
  expect(host.textContent).toContain('Gmail: Notifications only')
  await act(async () => (host.querySelector('[aria-label="Show permissions for me@x.com"]') as HTMLButtonElement).click())
  expect(host.querySelector('table')!.textContent).toContain('Read, Send')
  expect(host.textContent).toContain('Gmail reading is granted by Google but disabled in AgentWorks')
})

it('recognizes broader Google grants without treating restricted scopes as full access', () => {
  const grant = googleGrantedAccess(['https://www.googleapis.com/auth/drive', 'https://www.googleapis.com/auth/gmail.compose'])
  expect(grant.levels.docs).toBe('write')
  expect(grant.gmailRead).toBe(false)
  expect(grant.gmailWrite).toBe(true)
  const restricted = googleGrantedAccess(['https://www.googleapis.com/auth/drive.file', 'https://www.googleapis.com/auth/gmail.metadata'])
  expect(restricted.levels.drive).toBe('off')
  expect(restricted.gmailRead).toBe(false)
})
