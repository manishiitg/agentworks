// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ComposerPendingPrompt } from './ComposerPendingPrompt'
import { codingAgentQuestionCards, questionsOfThisTurn, SKIPPED_QUESTION_ANSWER } from '../../utils/codingAgentQuestions'
import type { PollingEvent } from '../../services/api-types'
import type { CodingAgentChoiceAnswer, CodingAgentQuestionPrompt } from '../../utils/codingAgentQuestions'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// #3076: multiple questions overflowed the docked composer. Navigate the real
// composer card, retain earlier choices, and never send until the explicit final action.
it('walks questions without losing answers or submitting from a choice shortcut', async () => {
  const prompt: CodingAgentQuestionPrompt = { provider: 'codex-cli', promptId: 'question-flow', state: 'pending', answers: [], questions: [
    { id: 'first', header: 'First', question: 'Which direction?', multiSelect: false, options: [{ label: 'Alpha', description: '' }, { label: 'Beta', description: '' }] },
    { id: 'second', header: 'Second', question: 'Which checks?', multiSelect: true, maxSelections: 2, options: [{ label: 'One', description: '' }, { label: 'Two', description: '' }] },
  ] }
  const replies: CodingAgentChoiceAnswer[][] = []
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const render = () => act(async () => root.render(<ComposerPendingPrompt prompt={prompt} onAnswer={async (_provider, _id, answers) => { replies.push(answers) }} />))
  const click = async (text: string) => act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === text)!.click())
  try {
    await render()
    expect(host.querySelectorAll('fieldset')).toHaveLength(1)
    expect(host.textContent).toContain('Question 1 of 2')
    await act(async () => host.querySelector<HTMLElement>('[data-testid="coding-agent-question-card"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: '2', bubbles: true })))
    expect(replies).toHaveLength(0)
    await click('Next')
    expect(host.textContent).toContain('Question 2 of 2')
    await click('Back')
    expect(host.querySelector<HTMLInputElement>('input[value="Beta"]')!.checked).toBe(true)
    await click('Next')
    await act(async () => host.querySelector<HTMLInputElement>('input[value="One"]')!.click())
    expect(replies).toHaveLength(0)
    await click('Send answer')
    expect(replies).toEqual([[{ id: 'first', selectedLabels: ['Beta'] }, { id: 'second', selectedLabels: ['One'] }]])
    // Settled history still shows every original question/answer, not just the final page.
    prompt.state = 'answered'; prompt.answers = replies[0]
    await render()
    expect(host.querySelectorAll('fieldset')).toHaveLength(2)
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})

// #3076: a restored question can be outside the loaded history page. Its new
// message-mode carrier includes the question, but loading the original cannot duplicate it.
it('shows a restored ordinary question without treating its id as a permission approval', async () => {
  const question = { id: 'command_permission', header: 'Choice', question: 'Which file name?', options: [{ label: 'Draft', description: '' }, { label: 'Final', description: '' }] }
  const event = (id: string, kind: string): PollingEvent => ({ id, type: 'coding_agent_question', session_id: 'restored-chat', data: { data: {
    provider: 'claude-code', prompt_id: 'clarification:restored', kind, questions: [question], metadata: { native_request_kind: 'question', message: 'The app restarted. Your answer continues this chat as a new message.' },
  } } } as PollingEvent)
  const requested = event('requested', 'requested'), recovered = event('recovered', 'recovered')
  const page = codingAgentQuestionCards([recovered])
  expect(page.cards.size).toBe(1)
  const full = codingAgentQuestionCards([requested, recovered])
  expect(full.cards.size).toBe(1)
  expect(full.hiddenEvents.has('recovered')).toBe(true)
  const prompt = [...page.cards.values()][0]
  expect(prompt.requestKind).toBe('question')
  const host = document.createElement('div'), root = createRoot(host)
  const answers: CodingAgentChoiceAnswer[][] = []
  try {
    await act(async () => root.render(<ComposerPendingPrompt prompt={prompt} onAnswer={async (_provider, _id, value) => { answers.push(value) }} />))
    expect(host.textContent).toContain('The app restarted')
    expect(host.textContent).not.toContain('Approval needed')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Final')!.click())
    expect(answers).toEqual([[{ id: 'command_permission', selectedLabels: ['Final'] }]])
  } finally { await act(async () => root.unmount()) }
})

// #3711: a plan is one slim line; the whole plan opens in the plan pane, and the answered record shrinks.
it('shows a pending plan as one line, opens the full plan in the pane, and shrinks once approved', async () => {
  const plan = '# Add rate limiting\n\nUploads have no limit.\n\n1. Add a limiter.\n2. Wire it in.\n3. Last step only in the pane.'
  const prompt: CodingAgentQuestionPrompt = { provider: 'claude-code', promptId: 'plan:1', state: 'pending', requestKind: 'plan_approval', answers: [], questions: [
    { id: 'plan', header: '', question: `Approve this plan?\n${plan}`, multiSelect: false, allowOther: true, options: [{ label: 'Approve', description: '' }, { label: 'Reject', description: '' }] },
  ] }
  const replies: CodingAgentChoiceAnswer[][] = []
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const button = (scope: ParentNode, text: string) => [...scope.querySelectorAll('button')].find(item => item.textContent === text)!
  try {
    await act(async () => root.render(<ComposerPendingPrompt prompt={prompt} onAnswer={async (_provider, _id, answers) => { replies.push(answers) }} />))
    const line = host.querySelector('[data-testid="plan-line"]')!
    expect(line.textContent).toContain('Add rate limiting')
    expect(line.textContent).toContain('3 steps')
    expect(line.textContent).toContain('Uploads have no limit.')
    expect(host.textContent).not.toContain('Last step only in the pane')
    // Claude's plan card takes typed changes from the message box (#3711).
    expect(line.textContent).toContain('type changes')
    await act(async () => button(host, 'View plan').click())
    const pane = document.querySelector('[data-testid="plan-pane"]')!
    expect(pane.textContent).toContain('Last step only in the pane')
    await act(async () => button(pane, 'Approve').click())
    expect(replies).toEqual([[{ id: 'plan', selectedLabels: ['Approve'] }]])
    expect(document.querySelector('[data-testid="plan-pane"]')).toBeNull()
    const { CodingAgentQuestionCard } = await import('../CodingAgentQuestionCard')
    await act(async () => root.render(<CodingAgentQuestionCard prompt={{ ...prompt, state: 'answered', answers: replies[0] }} />))
    expect(host.querySelector('[data-testid="plan-line"]')!.getAttribute('data-outcome')).toBe('approved')
    expect(host.textContent).toContain('Plan approved')
    await act(async () => root.render(<CodingAgentQuestionCard prompt={{ ...prompt, state: 'answered', answers: [{ id: 'plan', selectedLabels: [], otherText: 'Use 30 a minute' }] }} />))
    expect(host.querySelector('[data-testid="plan-line"]')!.getAttribute('data-outcome')).toBe('changed')
    await act(async () => root.render(<CodingAgentQuestionCard prompt={{ ...prompt, state: 'answered', answers: replies[0] }} />))
    expect(host.textContent).not.toContain('Uploads have no limit.')
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})

// The owner was stuck on a question with no way out: a question that takes typed answers can be skipped,
// and Skip answers every question so the agent carries on.
it('skips a question the person does not want to answer', async () => {
  const prompt: CodingAgentQuestionPrompt = { provider: 'claude-code', promptId: 'skip-me', state: 'pending', answers: [], questions: [
    { id: '0', header: '', question: 'Which mockup?', multiSelect: false, allowOther: true, options: [{ label: 'A', description: '' }, { label: 'B', description: '' }] },
    { id: '1', header: '', question: 'Which colour?', multiSelect: false, allowOther: true, options: [{ label: 'Red', description: '' }] },
  ] }
  const replies: CodingAgentChoiceAnswer[][] = []
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  try {
    await act(async () => root.render(<ComposerPendingPrompt prompt={prompt} onAnswer={async (_provider, _id, answers) => { replies.push(answers) }} />))
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="coding-agent-question-skip"]')!.click())
    expect(replies).toEqual([[{ id: '0', selectedLabels: [], otherText: SKIPPED_QUESTION_ANSWER }, { id: '1', selectedLabels: [], otherText: SKIPPED_QUESTION_ANSWER }]])
  } finally {
    act(() => root.unmount())
    host.remove()
  }
})

// An answered question from an earlier turn whose settlement was outside the loaded
// history was pinned above the message box on every new turn. Only this turn's
// questions, or ones the server reloaded after a restart, can be waiting now.
it('pins only questions from the running turn or reloaded after a restart', () => {
  const turnStart = Date.parse('2026-01-02T10:00:00Z')
  const event = (promptId: string, kind: string, timestamp: string): PollingEvent => ({ id: `${promptId}:${kind}`, type: 'coding_agent_question', timestamp, data: { data: { provider: 'codex-cli', prompt_id: promptId, kind } } } as PollingEvent)
  const keys = questionsOfThisTurn([
    event('old', 'requested', '2026-01-01T09:00:00Z'),
    event('restored', 'recovered', '2026-01-01T09:00:00Z'),
    event('now', 'requested', '2026-01-02T10:00:05Z'),
  ], turnStart)
  expect([...keys].sort()).toEqual(['codex-cli:now', 'codex-cli:restored'])
  // Before the turn's start is known, the person's last message starts it: an old question does not flash.
  const early = questionsOfThisTurn([
    event('old', 'requested', '2026-01-01T09:00:00Z'),
    { id: 'u', type: 'user_message', timestamp: '2026-01-02T10:00:00Z', data: {} } as PollingEvent,
  ], undefined)
  expect([...early]).toEqual([])
})
