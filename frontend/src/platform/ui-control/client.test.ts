import { describe, it, expect } from 'vitest'
import { supportedAction } from './client'

const base = { request_id: 'test', expires_at: '2030-01-01T00:00:00Z' }
// Owner, 3 Nov: the agent's UI tool shows only the Dashboard.
describe('closed semantic UI control contract', () => {
  it('opens and refreshes only the Dashboard', () => {
    expect(supportedAction({ ...base, view: 'report', action: 'open' })).toBe(true)
    expect(supportedAction({ ...base, view: 'report', action: 'refresh' })).toBe(true)
    expect(supportedAction({ ...base, view: 'report', action: 'open', target: 'Suites' })).toBe(true)
    expect(supportedAction({ ...base, view: 'report', action: 'send' })).toBe(false)
    for (const view of ['flow', 'files', 'mcp', 'notify', 'unknown']) {
      expect(supportedAction({ ...base, view, action: 'open' })).toBe(false)
    }
  })
  it('uses the exact server contract it is given', () => {
    const action = { ...base, view: 'memory', action: 'open', target: 'skills' }
    const crew = { version: 1, product: 'work', views: [{ id: 'memory', label: 'Memory & skills', actions: ['open'], target_kind: 'view_section', targets: ['memory', 'skills'] }] }
    expect(supportedAction(action)).toBe(false)
    expect(supportedAction(action, crew)).toBe(true)
    expect(supportedAction({ ...action, target: 'guessed' }, crew)).toBe(false)
  })
})
