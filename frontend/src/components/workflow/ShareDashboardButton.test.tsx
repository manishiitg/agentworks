// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ShareDashboardButton, ACCESS_CHAT_REQUEST } from './ShareDashboardButton'
const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post }, authApi: { getWorkflowAccess: vi.fn() } }))

it('combines owners, audience and selected agents; changes go to the provided chat', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  post.mockImplementation(async (_url, body) => ({ data: body.action === 'owners'
    ? { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [{ id: 'colleague', email: 'colleague@example.com' }], can_manage: true }
    : { mode: 'people', can_share: true, people: [{ id: 'user', email: 'user@example.com' }], agents: [{ chat_id: 'coach', name: 'Coach', purpose: 'Review feedback', selected: true }, { chat_id: 'private', name: 'Private work', selected: false }] } }))
  const host = document.createElement('div'), root = createRoot(host), onAsk = vi.fn()
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Crew/example" variant="panel" onAsk={onAsk} />))
    expect(host.querySelector('[aria-label="Current owners"]')?.textContent).toContain('colleague@example.com')
    expect(host.querySelector('[aria-label="Who can use"]')?.textContent).toContain('user@example.com')
    expect(host.querySelector('[aria-label="Shared agents"]')?.textContent).toContain('Coach')
    expect(host.textContent).not.toContain('Private work')
    expect(host.textContent).toContain('messages from sharing onward')
    expect(host.querySelector('input,textarea,select')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onAsk).toHaveBeenCalledExactlyOnceWith(ACCESS_CHAT_REQUEST)
    expect(post.mock.calls.every(([, body]) => ['share_status', 'owners'].includes(body.action))).toBe(true)
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
