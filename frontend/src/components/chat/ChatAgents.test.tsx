// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, expect, it } from 'vitest'
import { ChatAgentsSection, ChatAgentsStrip, useChatHelpersStore } from './ChatAgents'
import type { ChatHelper } from '../../utils/chatHelpers'

const started = Date.now() - 60_000
const helpers: ChatHelper[] = [
  { key: 'a', provider: 'codex-cli', name: 'Write the new tests', task: 'Write the new tests', activity: 'Running tests', state: 'running', startedAt: started },
  { key: 'b', provider: 'claude-code', name: 'Explore checkout code', task: '', state: 'done', startedAt: started - 600_000, endedAt: started - 300_000 },
  { key: 'c', provider: 'cursor-cli', name: 'Check the styles', task: '', state: 'done', startedAt: started - 900_000, endedAt: started - 800_000 },
  { key: 'd', provider: 'claude-code', name: 'Find the coupon code', task: '', state: 'failed', startedAt: started - 1_200_000 },
  { key: 'e', provider: 'agy-cli', name: 'Review the design', task: '', state: 'done', startedAt: started - 1_500_000 },
]

beforeEach(() => { useChatHelpersStore.setState({ lists: {}, selected: {} }) })

// #3245: chips name each helper and its app, not "Agents (N)".
it('names running helpers with their app above the message box', () => {
  const strip = renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={helpers} />)
  expect(strip).toContain('Codex')
  expect(strip).toContain('Write the new tests')
  expect(strip).toContain('Running tests')
  expect(strip).not.toContain('Explore checkout code') // finished helpers stay in the side pane
  expect(strip).not.toContain('more')
  expect(strip).not.toContain('Agents (')
})

it('shows nothing above the message box when no helper runs', () => {
  expect(renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={helpers.slice(1)} />)).toBe('')
})

async function renderSection(): Promise<{ host: HTMLDivElement; done: () => Promise<void> }> {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<ChatAgentsSection sessionId="chat" />))
  return { host, done: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('lists only running helpers in the side pane and opens one in place', async () => {
  const empty = await renderSection()
  expect(empty.host.textContent).toBe('')
  await empty.done()
  useChatHelpersStore.setState({ lists: { chat: helpers.slice(1) } })
  const idle = await renderSection()
  expect(idle.host.textContent).toBe('') // nothing running: no section (owner, 19 Nov)
  await idle.done()
  useChatHelpersStore.setState({ lists: { chat: helpers } })
  const { host, done } = await renderSection()
  try {
    const text = host.textContent || ''
    expect(text).toContain('Agents in this chat')
    expect(text).toContain('1 running')
    expect(text).not.toContain('done')
    expect(text).not.toContain('Explore checkout code') // finished helpers are not listed
    const row = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Write the new tests'))!
    await act(async () => { row.click() })
    expect(useChatHelpersStore.getState().selected.chat).toBe('a')
    expect(host.textContent).toContain('All agents')
    expect(host.textContent).toContain('Ask the agent about it')
    const back = [...host.querySelectorAll('button')].find(button => button.textContent === 'All agents')!
    await act(async () => { back.click() })
    expect(host.textContent).toContain('Write the new tests')
  } finally { await done() }
})

it('opens a handed-off task\'s tab and stops its turn', async () => {
  const linked: ChatHelper = { key: 'tab:m1', provider: 'codex-cli', name: 'Write the new tests', task: 'Write the new tests', state: 'running', startedAt: started, linkedChat: { key: 'crew:chat:codex', sessionId: 's2', agent: 'Codex tests', running: true } }
  useChatHelpersStore.setState({ lists: { chat: [linked] }, selected: { chat: 'tab:m1' } })
  const { host, done } = await renderSection()
  try {
    expect(host.textContent).toContain('in the tab "Codex tests"')
    expect(host.textContent).toContain('Open its tab')
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Stop')).toBe(true)
  } finally { await done() }
})
