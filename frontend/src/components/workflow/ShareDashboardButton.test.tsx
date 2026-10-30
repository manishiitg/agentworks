// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ShareDashboardButton, ACCESS_CHAT_REQUEST } from './ShareDashboardButton'
const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post }, authApi: { getWorkflowAccess: vi.fn() } }))

it('Crew access: one People list, owner moves use the owner operation, and only the company-wide choice opens it to everyone', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  post.mockReset()
  post.mockImplementation(async (_url, body) => {
    if (body.action === 'owners') return { data: { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [{ id: 'colleague', email: 'colleague@example.com' }], can_manage: true } }
    if (body.action === 'owner_remove') return { data: { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [], can_manage: true } }
    if (body.action === 'list') return { data: { dashboards: [{ dashboard_id: 'other', short_url: 'https://example.com/d/zzzzz' }, { dashboard_id: 'overview', short_url: 'https://example.com/d/abc23' }] } }
    if (body.action === 'share') return { data: { mode: body.mode, can_share: true, people: (body.emails || []).map((email: string, index: number) => ({ id: `u${index}`, email })) } }
    return { data: { mode: 'people', can_share: true, people: [{ id: 'user', email: 'user@example.com' }], agents: [{ chat_id: 'coach', name: 'Coach', purpose: 'Review feedback', selected: true }, { chat_id: 'private', name: 'Private work', purpose: '', selected: false }] } }
  })
  const host = document.createElement('div'), root = createRoot(host), onAsk = vi.fn()
  const button = (text: string) => [...host.querySelectorAll('button')].find(item => item.textContent?.includes(text))!
  const people = () => host.querySelector('[aria-label="People with access"]')?.textContent || ''
  const choose = async (select: HTMLSelectElement, value: string) => act(async () => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/example" variant="panel" onAsk={onAsk} />))
    expect(people()).toContain('Owner (created this)')
    expect(people()).toContain('colleague@example.com')
    expect(people()).toContain('user@example.com')
    expect(host.textContent).toContain('Agents others can message (1)')
    expect(host.textContent).not.toContain('Private work')
    expect(host.querySelector<HTMLSelectElement>('#company-access')?.value).toBe('none')

    await act(async () => button('Copy link').click())
    expect(writeText).toHaveBeenCalledExactlyOnceWith('https://example.com/d/abc23')

    const input = host.querySelector<HTMLInputElement>('input[type="email"]')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'New@Example.com')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => button('Add').click())
    expect(post).toHaveBeenCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'share', mode: 'people', emails: ['user@example.com', 'new@example.com'] })

    // Removing named people changes only the named list; it never opens or closes the Crew for the company.
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Remove user@example.com"]')!.click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'share', mode: 'people', emails: ['new@example.com'] })

    // Owner -> Can use goes through the owner operation, then adds them to the named list.
    await choose(host.querySelector<HTMLSelectElement>('select[aria-label="Access for colleague@example.com"]')!, 'use')
    expect(post).toHaveBeenCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'owner_remove', emails: ['colleague@example.com'] })
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'share', mode: 'people', emails: ['new@example.com', 'colleague@example.com'] })

    // Only the company-wide choice switches to everyone.
    await choose(host.querySelector<HTMLSelectElement>('#company-access')!, 'use')
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Crew/example', action: 'share', mode: 'everyone' })

    await act(async () => button('Or change access in chat').click())
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


it('asks before creating a missing Crew dashboard and keeps cancelling free of writes', async () => {
  post.mockReset()
  post.mockImplementation(async (_url, body) => {
    if (body.action === 'owners') return { data: { co_owners: [], can_manage: true } }
    if (body.action === 'share') {
      if (!body.create_dashboard) throw { response: { data: { dashboard_required: true } } }
      return { data: { mode: body.mode, can_share: true } }
    }
    return { data: { mode: 'private', can_share: true, needs_dashboard: true } }
  })
  const host = document.createElement('div'), root = createRoot(host)
  const chooseEveryone = async () => act(async () => {
    const select=host.querySelector<HTMLSelectElement>('#company-access')!
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')!.set!.call(select,'use')
    select.dispatchEvent(new Event('change',{bubbles:true}))
  })
  const button = (text:string) => [...host.querySelectorAll('button')].find(item=>item.textContent===text)!
  try {
    await act(async()=>root.render(<ShareDashboardButton workspacePath="Crew/example" variant="panel"/>))
    await chooseEveryone()
    expect(host.textContent).toContain('Create a dashboard for this Crew?')
    expect(host.querySelector<HTMLSelectElement>('#company-access')?.value).toBe('none')
    const calls=post.mock.calls.length
    await act(async()=>button('Cancel').click())
    expect(post.mock.calls).toHaveLength(calls)
    expect(host.textContent).not.toContain('Create a dashboard for this Crew?')
    await chooseEveryone()
    await act(async()=>button('Create dashboard and share').click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards',{workspace:'Crew/example',action:'share',mode:'everyone',create_dashboard:true})
    expect(host.querySelector<HTMLSelectElement>('#company-access')?.value).toBe('use')
    expect(host.textContent).not.toContain('Create a dashboard for this Crew?')
  } finally {await act(async()=>root.unmount())}
})
