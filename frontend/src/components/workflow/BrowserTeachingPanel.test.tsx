// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { BrowserTeachingPanel, type TeachState } from './BrowserTeachingPanel'
const api = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: api }))
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn())
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})
async function mount(initial: TeachState) {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  api.post.mockImplementation(async (_url, body) => ({
    data:
      body.action === 'list'
        ? { demonstrations: [] }
        : { ...initial, status: body.action === 'test' ? 'tested' : 'draft' },
  }))
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  function Harness() {
    const [state, setState] = useState(initial)
    return (
      <BrowserTeachingPanel
        workspacePath="Workflow/one"
        session="browser-one"
        state={state}
        onState={setState}
        onStart={vi.fn()}
        onControl={vi.fn()}
        onClose={vi.fn()}
      />
    )
  }
  cleanups.push(() => {
    act(() => root.unmount())
    host.remove()
  })
  await act(async () => {
    root.render(<Harness />)
  })
  return host
}
const draft: TeachState = {
  id: 'demo',
  goal: 'Greet customer',
  status: 'draft',
  actions: [
    {
      id: 1,
      kind: 'fill',
      target: { name: 'Customer' },
      value: 'Alice',
      parameter: 'customer',
    },
  ],
  check: { kind: 'text', value: 'Hello' },
  guidance: 'Greet the requested customer.',
}
it('tests the reviewed draft using the parameter example and prevents publishing edited checks', async () => {
  const host = await mount(draft)
  const value = host.querySelector(
    '[aria-label="Test value for customer"]',
  ) as HTMLInputElement
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(value, 'Bob')
    value.dispatchEvent(new Event('input', { bubbles: true }))
  })
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.textContent === 'Test procedure',
  )!
  await act(async () => {
    button.click()
  })
  expect(
    api.post.mock.calls.find(([, body]) => body.action === 'save')?.[1],
  ).toMatchObject({
    actions: draft.actions,
    check: draft.check,
    guidance: draft.guidance,
  })
  expect(
    api.post.mock.calls.find(([, body]) => body.action === 'test')?.[1].inputs,
  ).toEqual({ customer: 'Bob' })
  const publish = [...host.querySelectorAll('button')].find(
    (b) => b.textContent === 'Save for reuse',
  )!
  expect(publish.disabled).toBe(false)
  const check = host.querySelector(
    '[aria-label="Expected outcome"]',
  ) as HTMLInputElement
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(check, 'Different result')
    check.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(publish.disabled).toBe(true)
  expect(host.textContent).toContain('Test the edited procedure again')
})
it('keeps the recorder controls compact and hides the draft editor while demonstrating', async () => {
  const host = await mount({ ...draft, status: 'recording' })
  const dialog = host.querySelector('[role="dialog"]')!
  expect(dialog.className).toContain('bottom-2')
  expect(dialog.className).not.toContain('inset-x-2')
  expect(host.querySelector('[aria-label="Reviewed guidance"]')).toBeNull()
  expect(host.textContent).toContain('Recording actions')
  expect(
    [...host.querySelectorAll('button')].find((b) => b.textContent === 'Close')!
      .disabled,
  ).toBe(true)
})
