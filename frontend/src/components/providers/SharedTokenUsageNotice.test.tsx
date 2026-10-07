// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const usage = vi.hoisted(() => ({
  timezone: 'UTC', daily_used: 0, weekly_used: 0, day_resets_at: '2026-10-08T00:00:00Z', week_resets_at: '2026-10-12T00:00:00Z', state: 'ok',
  accounts: { 'muse-cli': { label: 'Muse', daily_used: 32, weekly_used: 70, daily_limit: 100, weekly_limit: 100, state: 'ok' } },
}))
vi.mock('../../services/api', () => ({ authApi: { getMyTokenUsage: vi.fn(async () => usage) } }))

import { SharedTokenUsageNotice } from './SharedTokenUsageNotice'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('names the account kind and only the limits that exist', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const text = () => host.querySelector('[data-testid="account-limits"]')?.textContent
  await act(async () => root.render(<SharedTokenUsageNotice accountProvider="muse-cli" />))
  expect(text()).toBe('Shared account · limits: Day 32% · Week 70% (resets day 00:00 UTC, week Monday 00:00 UTC)')
  await act(async () => root.render(<SharedTokenUsageNotice accountProvider="codex-cli" />))
  expect(text()).toBe('Shared account · no limit')
  await act(async () => root.render(<SharedTokenUsageNotice accountProvider={null} />))
  expect(text()).toBe('Your own account · not limited')
  act(() => root.unmount())
})
