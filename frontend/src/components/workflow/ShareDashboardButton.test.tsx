// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ShareDashboardButton } from './ShareDashboardButton'
const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post }, authApi: { getWorkflowAccess: vi.fn() } }))

it('Sharing a Goal or Flow: Shared with (Only me / Chosen people / Everyone) and Co-owners, each through its own server operation', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  post.mockReset()
  post.mockImplementation(async (_url, body) => {
    if (body.action === 'owners') return { data: { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [{ id: 'colleague', email: 'colleague@example.com' }], can_manage: true } }
    if (body.action === 'owner_remove') return { data: { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [], can_manage: true } }
    if (body.action === 'list') return { data: { dashboards: [{ dashboard_id: 'other', short_url: 'https://example.com/d/zzzzz' }, { dashboard_id: 'overview', short_url: 'https://example.com/d/abc23' }] } }
    if (body.action === 'share') return { data: { mode: body.mode === 'people' && !body.emails?.length ? 'private' : body.mode, can_share: true, has_owners: true, people: (body.emails || []).map((email: string, index: number) => ({ id: `u${index}`, email })) } }
    return { data: { mode: 'people', can_share: true, has_owners: true, people: [{ id: 'user', email: 'user@example.com' }] } }
  })
  const host = document.createElement('div'), root = createRoot(host)
  const button = (text: string) => [...host.querySelectorAll('button')].find(item => item.textContent === text)!
  const checked = () => host.querySelector('[role="radio"][aria-checked="true"]')?.textContent
  const submit = async (label: string, value: string) => {
    const input = host.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => input.form!.requestSubmit())
  }
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Workflow/example" variant="panel" />))
    expect(checked()).toBe('Chosen people')
    expect(host.querySelector('[aria-label="People it is shared with"]')?.textContent).toContain('user@example.com')
    expect(host.querySelector('[aria-label="Owners"]')?.textContent).toContain('Created it')
    expect(host.querySelector('[aria-label="Owners"]')?.textContent).toContain('colleague@example.com')

    await act(async () => button('Copy dashboard link').click())
    expect(writeText).toHaveBeenCalledExactlyOnceWith('https://example.com/d/abc23')

    await submit('Share with a person by email', 'New@Example.com')
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Workflow/example', action: 'share', mode: 'people', emails: ['user@example.com', 'new@example.com'] })

    // Removing a named person changes only the named list.
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Remove user@example.com"]')!.click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Workflow/example', action: 'share', mode: 'people', emails: ['new@example.com'] })

    // Co-owners use the owner operation, never the share list.
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Remove colleague@example.com"]')!.click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Workflow/example', action: 'owner_remove', emails: ['colleague@example.com'] })

    await act(async () => button('Everyone').click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Workflow/example', action: 'share', mode: 'everyone' })
    expect(checked()).toBe('Everyone')

    // Only me clears the named list; the server stores that as private.
    await act(async () => button('Only me').click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', { workspace: 'Workflow/example', action: 'share', mode: 'people', emails: [] })
    expect(checked()).toBe('Only me')
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
    if (body.action === 'share') {
      if (!body.create_dashboard) throw { response: { data: { dashboard_required: true } } }
      return { data: { mode: body.mode, can_share: true } }
    }
    return { data: { mode: 'private', can_share: true, needs_dashboard: true } }
  })
  const host = document.createElement('div'), root = createRoot(host)
  const button = (text:string) => [...host.querySelectorAll('button')].find(item=>item.textContent===text)!
  const chooseEveryone = async () => act(async () => button('Everyone').click())
  try {
    await act(async()=>root.render(<ShareDashboardButton workspacePath="Crew/example" variant="panel"/>))
    // A Crew has no owner list: its builders are its Team.
    expect(host.querySelector('[aria-label="Co-owners"]')).toBeNull()
    expect(post.mock.calls.some(([, body]) => body.action === 'owners')).toBe(false)
    await chooseEveryone()
    expect(host.textContent).toContain('Create a dashboard for this Crew?')
    expect(host.querySelector('[role="radio"][aria-checked="true"]')?.textContent).toBe('Only me')
    const calls=post.mock.calls.length
    await act(async()=>button('Cancel').click())
    expect(post.mock.calls).toHaveLength(calls)
    expect(host.textContent).not.toContain('Create a dashboard for this Crew?')
    await chooseEveryone()
    await act(async()=>button('Create dashboard and share').click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards',{workspace:'Crew/example',action:'share',mode:'everyone',create_dashboard:true})
    expect(host.querySelector('[role="radio"][aria-checked="true"]')?.textContent).toBe('Everyone')
    expect(host.textContent).not.toContain('Create a dashboard for this Crew?')
  } finally {await act(async()=>root.unmount())}
})
