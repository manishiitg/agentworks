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
    expect.stringContaining('Team agent'), expect.stringContaining('Review'), expect.stringContaining('Checks'),
  ])
  expect(host.textContent).toContain('typing… Checking the layout')
  expect(disclosure('Agents').textContent).toContain('3 · 2 online')
  expect(host.textContent).not.toContain('My work')
  expect(host.textContent).not.toContain('more of yours')
  await act(async () => (rows[1] as HTMLButtonElement).click())
  expect(ask).toHaveBeenCalledWith(agents[2]) // server-admitted row, not a constructed home identity
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
  const runtime = { cli: 'Coding CLI', model_id: 'model-from-server', reasoning_effort: 'high' }
  await render('server:owner:crew', agents.map(row => row.id === 'peer' ? { ...row, ...runtime } : row))
  const label = host.querySelector(`[title*="${runtime.model_id}"]`)
  expect(label?.textContent).toContain(runtime.cli)
  expect(label?.textContent).toContain(runtime.model_id)
  expect(label?.getAttribute('title')).toContain(runtime.reasoning_effort)
  expect(host.textContent).not.toContain('Other own work')
})

// New posts change the unread summary, never the person's disclosure choice.
it('keeps both groups closed when new activity arrives', async () => {
 await render()
 const next=[...posts,{id:'new-post',kind:'doing',person:'Colleague',agent:'Review',line:'Checking the new change',at:'2026-11-02T12:00:00Z'}]
 await act(async()=>root.render(<TeamPanel scope="server:owner:crew" agents={agents} posts={next} onAsk={ask} onActivity={activity}/>))
 expect(disclosure('Agents').getAttribute('aria-expanded')).toBe('false')
 expect(disclosure('Activity').getAttribute('aria-expanded')).toBe('false')
 expect(disclosure('Activity').textContent).toContain('2 new')
 expect(host.querySelector('[aria-label="Recent Team activity"]')).toBeNull()
 expect(host.querySelector('[aria-label="Team agents"]')).toBeNull()
})

it('explains offline asks only when the server supplied an admitted Team agent',async()=>{
 await render();await click('Agents')
 expect(host.textContent).toContain('Offline · new asks go to the Team agent')
 await render('server:owner:crew',agents.filter(row=>!row.team_agent))
 expect(host.textContent).not.toContain('new asks go to the Team agent')
 expect(host.textContent).toContain('Offline · no Team agent available')
})
