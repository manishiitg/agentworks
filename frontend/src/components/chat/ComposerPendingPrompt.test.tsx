// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ComposerPendingPrompt } from './ComposerPendingPrompt'
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
