// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ShareDashboardButton } from './ShareDashboardButton'

const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post } }))

it('shows the server message when a share is refused with a structured error', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const host = document.createElement('div')
  const root = createRoot(host)
  post.mockResolvedValueOnce({ data: { mode: 'private', can_share: true } })
  post.mockRejectedValueOnce({ response: { status: 404, data: {} } }) // the Owners section is for Crews only
  post.mockRejectedValueOnce({ response: { status: 409, data: { error: { code: 'conflict', message: 'Publish your unfinished dashboard, then share it.' } } } })
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Workflow/invoices" />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Share"]')!.click())
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Save')!.click())
    expect(host.querySelector('[role="status"]')?.textContent).toBe('Publish your unfinished dashboard, then share it.')
    expect(host.textContent).not.toContain('[object Object]')
  } finally {
    await act(async () => root.unmount())
  }
})

it('does not expose sharing controls when the server refuses sharing, including the inline host', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  post.mockResolvedValue({ data: { mode: 'private', can_share: false } })
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Workflow/reader" variant="row" label="Manage people & access" />))
    expect(host.querySelector('button')).toBeNull()
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  } finally {
    await act(async () => root.unmount())
  }
})

it('keeps people and owner controls separate and preserves owner-management admission', async () => {
  post.mockReset()
  post.mockImplementation(async (_url, body) => ({ data: body.action === 'owners'
    ? { owner: { id: 'owner' }, co_owners: [], can_manage: false }
    : { mode: 'private', can_share: true } }))
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/one" variant="panel" section="owners" />))
    expect(host.querySelector('[aria-label="Owners"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Add an owner by email"]')).toBeNull()
    expect(host.querySelector('input[type="radio"]')).toBeNull()
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Save')).toBe(false)
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/one" variant="panel" section="people" />))
    expect(host.querySelector('input[type="radio"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Owners"]')).toBeNull()
    expect(post.mock.calls.every(([, body]) => ['share_status', 'owners'].includes(body.action))).toBe(true)
  } finally { await act(async () => root.unmount()) }
})

it('shows owner roles and only permits management granted by the server', async () => {
  post.mockReset()
  post.mockImplementation(async (_url, body) => ({ data: body.action === 'share_status' ? { mode: 'private', can_share: true } : { owner: { id: 'main', username: 'Main person' }, co_owners: [{ id: 'second', email: 'second@example.com' }], can_manage: true } }))
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/one" variant="panel" section="owners" />))
    expect(host.querySelector('[aria-label="Current owners"]')?.textContent).toContain('Main personMain owner')
    expect(host.querySelector('[aria-label="Current owners"]')?.textContent).toContain('second@example.com')
    expect(host.querySelector('[aria-label="Owners"]')?.className).not.toContain('border-t')
    expect(host.textContent).toContain('including earlier messages')
    const input = host.querySelector<HTMLInputElement>('[aria-label="Add an owner by email"]')!
    const add = [...host.querySelectorAll('button')].find(button => button.textContent === 'Add owner')!
    expect(add.disabled).toBe(true)
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => { setter.call(input, 'new@example.com'); input.dispatchEvent(new Event('input', { bubbles: true })) })
    await act(async () => add.click())
    expect(post).toHaveBeenCalledWith('/api/dashboards', { workspace: 'Crew/one', action: 'owner_add', emails: ['new@example.com'] })
    expect(host.querySelector('input[type="radio"]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})

it('replaces a blank owner page with a retry when loading fails', async () => {
  post.mockReset()
  let failed = true
  post.mockImplementation(async (_url, body) => {
    if (body.action === 'share_status') return { data: { mode: 'private', can_share: true } }
    if (failed) throw new Error('offline')
    return { data: { owner: { id: 'main' }, co_owners: [], can_manage: false } }
  })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/one" variant="panel" section="owners" />))
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not load the owners')
    failed = false
    await act(async () => host.querySelector<HTMLButtonElement>('[role="alert"] button')!.click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.textContent).toContain('Main owner')
    expect(host.querySelector('[aria-label="Add an owner by email"]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})

it('saves selected existing agents with the same Share audience', async () => {
  post.mockReset()
  post.mockResolvedValue({ data: { mode: 'private', can_share: true, agents: [{ chat_id: 'team:chat:coach', name: 'Coach', purpose: 'Review feedback', selected: false, can_edit_purpose: true }] } })
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/team" variant="panel" section="people" />))
    expect(host.textContent).toContain('messages from sharing onward')
    await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Save')!.click())
    expect(post).toHaveBeenLastCalledWith('/api/dashboards', expect.objectContaining({ workspace: 'Crew/team', action: 'share', mode: 'private', shared_agents: ['team:chat:coach'], agent_purposes: {} }))
  } finally { await act(async () => root.unmount()) }
})
