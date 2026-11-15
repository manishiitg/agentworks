// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TeamPanel, type TeamPanelAgent } from './TeamPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div'); document.body.append(host)
const root = createRoot(host)
const ask = vi.fn(), activity = vi.fn()
const agents: TeamPanelAgent[] = [
  { id: 'own', name: 'My work', person: 'Owner', mine: true, online: true, place: 'mac' },
  { id: 'own2', name: 'Other own work', person: 'Owner', mine: true, online: true, place: 'mac' },
  { id: 'peer', name: 'Review', person: 'Colleague', mine: false, online: true, place: 'mac', activity: 'running', typing: true, last_message: 'Checking the layout' },
  { id: 'away', name: 'Checks', person: 'Colleague', mine: false, online: false, place: 'mac', last_message: 'Checks finished', last_message_at: '2026-11-01T12:00:00Z' },
  { id: 'home', name: 'Team agent', person: 'Team', mine: false, team_agent: true, online: true, place: 'server' },
]
const posts = [{ id: 'post', kind: 'decision', agent: 'Review', person: 'Colleague', line: 'Use the shared tool', at: '2026-11-01T12:00:00Z' }]
const render = async (scope = 'server:owner:crew', rows = agents) => act(async () => root.render(<TeamPanel scope={scope} agents={rows} posts={posts} onAsk={ask} onActivity={activity} />))
const disclosure = (name: string) => [...host.querySelectorAll('button[aria-expanded]')].find(button => button.textContent?.startsWith(name)) as HTMLButtonElement
const click = async (name: string) => act(async () => disclosure(name).click())
afterEach(async () => { await act(async () => root.render(null)); localStorage.clear(); vi.clearAllMocks() })

// Protect the person/account boundary while exercising the actual compact rows:
// no maker's transcript is opened by clicking, and UI never invents an offline route.
it('starts closed, remembers each person separately and opens the admitted agent', async () => {
  await render()
  expect(disclosure('Agents').getAttribute('aria-expanded')).toBe('false')
  expect(disclosure('Activity').getAttribute('aria-expanded')).toBe('false')
  expect(host.querySelector('[aria-label="Team agents"]')).toBeNull()
  await click('Agents')
  const rows = [...host.querySelectorAll('[aria-label="Team agents"] > button')]
  expect(rows.map(row => row.textContent)).toEqual([
    expect.stringContaining('Team agent'), expect.stringContaining('Review'), expect.stringContaining('Checks'), expect.stringContaining('My work'), '+ 1 more of yours',
  ])
  expect(host.textContent).toContain('typing… Checking the layout')
  await act(async () => (rows[2] as HTMLButtonElement).click())
  expect(ask).toHaveBeenCalledWith(agents[3]) // server-admitted row, not a constructed home identity
  await click('Activity')
  expect(host.querySelector('[aria-label="Recent Team activity"]')?.textContent).toContain('Use the shared tool')
  await act(async () => [...host.querySelectorAll('button')].find(row => row.textContent === 'See all activity')!.click())
  expect(activity).toHaveBeenCalledOnce()
  await act(async () => root.render(null))
  await render()
  expect(disclosure('Agents').getAttribute('aria-expanded')).toBe('true')
  expect(disclosure('Activity').getAttribute('aria-expanded')).toBe('true')
  await render('server:other-person:crew')
  expect(disclosure('Agents').getAttribute('aria-expanded')).toBe('false')
  expect(disclosure('Activity').getAttribute('aria-expanded')).toBe('false')
})

it('replaces a live line from the existing feed projection and never reveals hidden own rows by default', async () => {
  await render(); await click('Agents')
  expect(host.textContent).not.toContain('Other own work')
  await render('server:owner:crew', agents.map(row => row.id === 'peer' ? { ...row, typing: false, last_message: 'The layout is ready', last_message_at: new Date().toISOString() } : row))
  expect(host.textContent).not.toContain('Checking the layout')
  expect(host.textContent).not.toContain('typing…')
  expect(host.textContent).toContain('now · The layout is ready')
  await click('+ 1 more of yours')
  expect(host.textContent).toContain('Other own work')
})
