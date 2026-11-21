// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { CrewRunCost, type RunStepCost } from './CrewRunCost'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function text(props: { total?: number; known: boolean; steps: RunStepCost[] }) {
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(<CrewRunCost {...props} />))
  const content = host.textContent ?? ''
  await act(async () => root.unmount())
  return content
}

it('shows the total and only the steps that cost something', async () => {
  const content = await text({ total: 0.0731, known: true, steps: [{ title: 'Review', cost: 0.07 }, { title: 'Fetch', cost: 0 }] })
  expect(content).toContain('$0.07')
  expect(content).toContain('Review')
  expect(content).not.toContain('Fetch')
})

it('says so when the run cost nothing, instead of showing nothing', async () => {
  expect(await text({ total: undefined, known: true, steps: [] })).toContain('No cost. This run made no paid model calls.')
  expect(await text({ total: 0, known: true, steps: [] })).toContain('No cost.')
})

it('says it is loading until the costs are known, and rounds tiny costs', async () => {
  expect(await text({ known: false, steps: [] })).toContain('Loading…')
  expect(await text({ total: 0.004, known: true, steps: [] })).toContain('<$0.01')
})
