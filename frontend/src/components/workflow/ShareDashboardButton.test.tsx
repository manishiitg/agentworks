// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ShareDashboardButton, ACCESS_CHAT_REQUEST } from './ShareDashboardButton'
const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post }, authApi: { getWorkflowAccess: vi.fn() } }))

it('shows owners, audience and agents; changes sharing, adds a person and copies the short link', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  post.mockReset()
  post.mockImplementation(async (_url, body) => {
    if (body.action === 'owners') return { data: { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [{ id: 'colleague', email: 'colleague@example.com' }], can_manage: true } }
    if (body.action === 'list') return { data: { dashboards: [{ dashboard_id: 'other', short_url: 'https://example.com/d/zzzzz' }, { dashboard_id: 'overview', short_url: 'https://example.com/d/abc23' }] } }
    if (body.action === 'share') return { data: { mode: body.mode, can_share: true, people: (body.emails || []).map((email: string, index: number) => ({ id: `u${index}`, email })) } }
    return { data: { mode: 'people', can_share: true, people: [{ id: 'user', email: 'user@example.com' }], agents: [{ chat_id: 'coach', name: 'Coach', purpose: 'Review feedback', selected: true }, { chat_id: 'private', name: 'Private work', purpose: '', selected: false }] } }
  })
  const host = document.createElement('div'), root = createRoot(host), onAsk = vi.fn()
  const button = (text: string) => [...host.querySelectorAll('button')].find(item => item.textContent?.includes(text))!
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/example" variant="panel" onAsk={onAsk} />))
    expect(host.querySelector('[aria-label="Current owners"]')?.textContent).toContain('colleague@example.com')
    expect(host.querySelector('[aria-label="People with access"]')?.textContent).toContain('user@example.com')
    expect(host.querySelector('[aria-label="Shared agents"]')?.textContent).toContain('Coach')
    expect(host.textContent).not.toContain('Private work')
    expect(host.querySelector<HTMLSelectElement>('#general-access')?.value).toBe('people')

    await act(async () => button('Copy link').click())
    expect(writeText).toHaveBeenCalledExactlyOnceWith('https://example.com/d/abc23')

    const opened = vi.fn()
    window.addEventListener('agentworks:open-user-view', opened)
    await act(async () => button('Open as a user').click())
    window.removeEventListener('agentworks:open-user-view', opened)
    expect((opened.mock.calls[0]?.[0] as CustomEvent).detail).toEqual({ workspacePath: 'Crew/example' })

    const input = host.querySelector<HTMLInputElement>('input[type="email"]')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'New@Example.com')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => button('Add').click())
    expect(post).toHaveBeenCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'share', mode: 'people', emails: ['user@example.com', 'new@example.com'] })
    expect(host.querySelector('[aria-label="People with access"]')?.textContent).toContain('new@example.com')

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Remove user@example.com"]')!.click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'share', mode: 'people', emails: ['new@example.com'] })

    await act(async () => [...host.querySelectorAll('button')].at(-1)!.click())
    expect(onAsk).toHaveBeenCalledExactlyOnceWith(ACCESS_CHAT_REQUEST)
  } finally { await act(async () => root.unmount()) }
})

it('keeps the server sharing admission and provides a retry for failed reads', async () => {
  post.mockReset(); post.mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ data: { can_share: false, mode: 'private' } })
  const host = document.createElement('div'), root = createRoot(host), onAsk = vi.fn()
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/example" variant="panel" onAsk={onAsk} />))
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not load access')
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(host.textContent).toContain('Ask a project owner')
    expect(host.querySelector('button')).toBeNull()
    expect(onAsk).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()) }
})
