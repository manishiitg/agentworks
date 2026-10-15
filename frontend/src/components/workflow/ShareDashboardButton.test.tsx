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
  post.mockRejectedValueOnce({ response: { status: 409, data: { error: { code: 'conflict', message: 'Publish your unfinished dashboard, then share it.' } } } })
  try {
    await act(async () => root.render(<ShareDashboardButton workspacePath="Workflow/invoices" />))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Share dashboard"]')!.click())
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Save')!.click())
    expect(host.querySelector('[role="status"]')?.textContent).toBe('Publish your unfinished dashboard, then share it.')
    expect(host.textContent).not.toContain('[object Object]')
  } finally {
    await act(async () => root.unmount())
  }
})
