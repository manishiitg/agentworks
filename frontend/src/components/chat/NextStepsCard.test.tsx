// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { NextStepsCard, NextStepsSlot, NextStepsSlotHost } from './NextStepsCard'
import type { NextOption } from '../../utils/nextSteps'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function mount(node: React.ReactNode) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const root = createRoot(el)
  act(() => root.render(node))
  return { el, render: (next: React.ReactNode) => act(() => root.render(next)) }
}

const text = (el: HTMLElement, id: string) => el.querySelector(`[data-testid="${id}"]`)?.textContent

it('shows the card in place when the chat has no slot on screen', () => {
  const { el } = mount(<div data-testid="composer"><NextStepsSlot tabId="tab-a"><p>card</p></NextStepsSlot></div>)
  expect(text(el, 'composer')).toBe('card')
})

it("moves the card into the chat's transcript slot, and back when the slot goes", () => {
  const card = <div data-testid="composer"><NextStepsSlot tabId="tab-b"><p>card</p></NextStepsSlot></div>
  const { el, render } = mount(<><div data-testid="transcript"><NextStepsSlotHost slotId="tab-b" /></div>{card}</>)
  expect(text(el, 'transcript')).toBe('card')
  expect(text(el, 'composer')).toBe('')
  render(<><div data-testid="transcript" />{card}</>)
  expect(text(el, 'composer')).toBe('card')
})

const goalSteps = { summary: '', question: 'How?', options: [{ text: 'Fix and rerun until green', goal: 'All workspace tests pass' }, { text: 'Plan the move', plan: 'Move history to the database' }, { text: 'Leave it' }] }

it('shows goal and plan answers as Start as goal / Plan first where the app has them, and sends the option', () => {
  const picked: NextOption[] = []
  const { el } = mount(<NextStepsCard steps={goalSteps} modes={{ goal: true, plan: true }} onPick={option => picked.push(option)} onDismiss={() => {}} />)
  const goal = el.querySelector<HTMLButtonElement>('[data-testid="next-steps-option-0"]')!
  expect(goal.dataset.mode).toBe('goal')
  expect(goal.textContent).toContain('Start as goal')
  expect(goal.textContent).toContain('All workspace tests pass')
  expect(el.querySelector<HTMLButtonElement>('[data-testid="next-steps-option-1"]')!.dataset.mode).toBe('plan')
  act(() => goal.click())
  expect(picked).toEqual([goalSteps.options[0]])
})

it('shows goal and plan answers as plain answers in an app without them', () => {
  const { el } = mount(<NextStepsCard steps={goalSteps} onPick={() => {}} onDismiss={() => {}} />)
  const goal = el.querySelector<HTMLButtonElement>('[data-testid="next-steps-option-0"]')!
  expect(goal.dataset.mode).toBeUndefined()
  expect(goal.textContent).toContain('Fix and rerun until green')
  expect(goal.textContent).not.toContain('Start as goal')
})
