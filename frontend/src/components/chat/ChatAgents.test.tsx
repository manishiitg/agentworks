import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { ChatAgentsStrip } from './ChatAgents'
import type { ChatHelper } from '../../utils/chatHelpers'

const started = Date.now() - 60_000
const longName = 'Remove features and task tracker from the Team panel and its menus'
const helpers: ChatHelper[] = [
  { key: 'a', provider: 'claude-code', name: longName, task: longName, activity: 'Running Trim the work tracker surfaces', state: 'running', startedAt: started, toolCallId: 'call-a', stopId: 'task:a' },
  { key: 'b', provider: 'claude-code', name: 'Explore checkout code', task: '', state: 'done', startedAt: started - 600_000, endedAt: started - 300_000, toolCallId: 'call-b' },
  { key: 'c', provider: 'cursor-cli', name: 'Check the styles', task: '', state: 'done', startedAt: started - 900_000 },
]

// #3245 (owner, 19 Nov): each running helper in full, with its actions on the line.
it('shows each running helper in full with Open conversation and Stop on its line', () => {
  const strip = renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={helpers} />)
  expect(strip).toContain('Claude')
  expect(strip).toContain(longName)
  expect(strip).toContain('Running Trim the work tracker surfaces')
  expect(strip).not.toContain('truncate')
  expect(strip).toContain('Open conversation')
  expect(strip).toContain('>Stop<')
  expect(strip).not.toContain('Explore checkout code') // finished helpers are not shown
})

it('shows nothing above the message box when no helper runs', () => {
  expect(renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={helpers.slice(1)} />)).toBe('')
})

it('lists three helpers, then +N more', () => {
  const many: ChatHelper[] = [1, 2, 3, 4, 5].map(n => ({ key: `r${n}`, provider: 'codex-cli', name: `Helper ${n}`, task: '', state: 'running', startedAt: started - n * 1000 }))
  const strip = renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={many} />)
  expect(strip.match(/data-testid="chat-agent-line"/g)).toHaveLength(3)
  expect(strip).toContain('+2 more')
})

it('opens a handed-off task\'s tab and stops its turn from the line', () => {
  const linked: ChatHelper = { key: 'tab:m1', provider: 'codex-cli', name: 'Write the new tests', task: 'Write the new tests', state: 'running', startedAt: started, linkedChat: { key: 'crew:chat:codex', sessionId: 's2', agent: 'Codex tests', running: true } }
  const strip = renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={[linked]} />)
  expect(strip).toContain('in the tab &quot;Codex tests&quot;')
  expect(strip).toContain('Open its tab')
  expect(strip).toContain('>Stop<')
})

it('does not repeat the name when the activity is the same text', () => {
  const same: ChatHelper = { key: 's', provider: 'claude-code', name: 'Remove features', task: 'Remove features', activity: 'Remove features', state: 'running', startedAt: started }
  const strip = renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={[same]} />)
  expect(strip.split('>Remove features<')).toHaveLength(2)
  expect(strip).not.toContain('· Remove features')
})

// Owner, 19 Nov: helpers from an old turn showed as running for a day.
it('drops a helper inside the turn when the chat is idle, keeps one with its own stop', () => {
  const stale: ChatHelper = { key: 'native:old', provider: 'claude-code', name: 'Old foreground helper', task: '', state: 'running', startedAt: started - 86_400_000 }
  const background: ChatHelper = { key: 'native:bg', provider: 'claude-code', name: 'Background helper', task: '', state: 'running', startedAt: started, stopId: 'claude-task:bg' }
  const idle = renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={[stale, background]} busy={false} />)
  expect(idle).not.toContain('Old foreground helper')
  expect(idle).toContain('Background helper')
  expect(renderToStaticMarkup(<ChatAgentsStrip sessionId="chat" helpers={[stale]} busy />)).toContain('Old foreground helper')
  expect(idle).toContain('Remove &quot;Background helper&quot; from this list')
})
