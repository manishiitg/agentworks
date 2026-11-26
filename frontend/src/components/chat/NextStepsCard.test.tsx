// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { NextStepsSlot, NextStepsSlotHost } from './NextStepsCard'

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
