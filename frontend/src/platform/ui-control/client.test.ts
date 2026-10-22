import { describe, it, expect } from 'vitest'
import { supportedAction } from './client'
import { UI_CONTROL_CONTRACT } from './contract.generated'

const base = { request_id: 'test', expires_at: '2030-01-01T00:00:00Z' }
describe('closed semantic UI control contract', () => {
  it('accounts for all views but never advertises placeholder deep actions', () => {
    expect(UI_CONTROL_CONTRACT.views).toHaveLength(20)
    for (const { id } of UI_CONTROL_CONTRACT.views) {
      expect(supportedAction({ ...base, view: id, action: 'open' })).toBe(true)
      expect(supportedAction({ ...base, view: id, action: 'send' })).toBe(false)
      expect(supportedAction({ ...base, view: id, action: 'open', target: 'guessed' })).toBe(id === 'flow' || id === 'report' || id === 'files')
      expect(supportedAction({ ...base, view: id, action: 'refresh' })).toBe(true)
    }
  })
  it('opens the restored Pulse sections without advertising extra operations', () => {
    for (const target of ['goals', 'metrics', 'activity', 'decisions', 'settings', 'chat']) expect(supportedAction({ ...base, view: 'pulse', action: 'open', target })).toBe(true)
    expect(supportedAction({ ...base, view: 'pulse', action: 'open', target: 'enable' })).toBe(false)
  })
  it('allows a bounded report tab target', () => {
    expect(supportedAction({ ...base, view: 'report', action: 'open', target: 'Suites' })).toBe(true)
  })
  it('advertises a workspace file target', () => {
    expect(supportedAction({ ...base, view: 'files', action: 'open', target: 'code/shared/helpers.py' })).toBe(true)
  })
  it('opens either section of the combined schedules view', () => {
    expect(supportedAction({ ...base, view: 'schedules', action: 'open', target: 'schedules' })).toBe(true)
    expect(supportedAction({ ...base, view: 'schedules', action: 'open', target: 'webhooks' })).toBe(true)
    expect(supportedAction({ ...base, view: 'schedules', action: 'open', target: 'guessed' })).toBe(false)
  })
  it('opens the bounded sections of the Automation center', () => {
    for (const target of ['chats', 'schedules', 'conditions', 'triggers', 'functions']) {
      expect(supportedAction({ ...base, view: 'workshop', action: 'open', target })).toBe(true)
    }
    expect(supportedAction({ ...base, view: 'workshop', action: 'open', target: 'guessed' })).toBe(false)
  })
  it('opens the bounded tabs of the Knowledge view', () => {
    for (const target of ['knowledgebase', 'database']) {
      expect(supportedAction({ ...base, view: 'knowledge', action: 'open', target })).toBe(true)
    }
    expect(supportedAction({ ...base, view: 'knowledge', action: 'open', target: 'guessed' })).toBe(false)
  })
  it('only expands the two known notification instruction disclosures', () => {
    expect(supportedAction({ ...base, view: 'notify', action: 'expand', target: 'run_summary' })).toBe(true)
    expect(supportedAction({ ...base, view: 'notify', action: 'expand', target: 'pulse_review' })).toBe(true)
    expect(supportedAction({ ...base, view: 'notify', action: 'expand', target: 'send-test' })).toBe(false)
    expect(supportedAction({ ...base, view: 'unknown', action: 'open' })).toBe(false)
  })
})


it('opens Secrets only as a bounded Connections section', () => {
  expect(supportedAction({ ...base, view: 'mcp', action: 'open', target: 'secrets' })).toBe(true)
  expect(supportedAction({ ...base, view: 'mcp', action: 'open', target: 'guessed' })).toBe(false)
  expect(supportedAction({ ...base, view: 'secrets', action: 'open' })).toBe(false)
})

it('uses the exact server contract for Crew views, without widening Goal views', () => {
  const action = { ...base, view: 'memory', action: 'open', target: 'skills' }
  const crew = { version: 1, product: 'work', views: [{ id: 'memory', label: 'Memory & skills', actions: ['open'], target_kind: 'view_section', targets: ['memory', 'skills'] }] }
  expect(supportedAction(action)).toBe(false)
  expect(supportedAction(action, crew)).toBe(true)
  expect(supportedAction({ ...action, target: 'guessed' }, crew)).toBe(false)
})
