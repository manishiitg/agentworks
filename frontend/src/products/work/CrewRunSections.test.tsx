// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { fileSizeLabel } from './CrewRunFiles'
import { CrewRunSummary } from './CrewRunSummary'
import { runStepLines } from './CrewWorkflowRuns'
import type { RunPythonStep } from '../../services/api-types'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function text(node: React.ReactElement) {
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(node))
  const content = host.textContent ?? ''
  await act(async () => root.unmount())
  return content
}

it('writes a file size people can read', () => {
  expect(fileSizeLabel(812)).toBe('812 B')
  expect(fileSizeLabel(3482)).toBe('3.4 KB')
  expect(fileSizeLabel(1.2 * 1024 * 1024)).toBe('1.2 MB')
})

it('says when a run started, how long it took and what it cost, or that it cost nothing', async () => {
  expect(await text(<CrewRunSummary facts={['Started 18:36', 'took 12s', 'by hand']} cost={0} costKnown steps={[]} />)).toBe('Started 18:36 · took 12s · by hand · No cost')
  const paid = await text(<CrewRunSummary facts={['Started 09:00']} cost={0.5} costKnown steps={[{ title: 'Review', cost: 0.5 }, { title: 'Fetch', cost: 0 }]} />)
  expect(paid).toContain('Cost $0.50')
  expect(paid).toContain('Review')
  expect(paid).not.toContain('Fetch')
  expect(await text(<CrewRunSummary facts={['Started 09:00']} costKnown={false} steps={[]} />)).toBe('Started 09:00')
})

const step = (id: string, name: string, file: string): RunPythonStep => ({ execution_id: id, id, name, source_call: `${file}:fn@${file}:1`, status: 'completed', started_at: '2026-01-01T00:00:00Z' })
const inspection = (id: string) => ({ status: 'completed', inspection: { session_id: 's', execution_id: id, name: 'Agent step', step_id: id } }) as never

it('marks a step the script labelled as scripted and one an agent ran as an agent', () => {
  const lines = runStepLines([inspection('a')], null, [step('p', 'Fetch', 'x.py')])
  expect(lines.map(line => [line.title, line.kind])).toEqual([['Agent step', 'agent'], ['Fetch', 'script']])
})

it('lets the source outline decide when it names the step', () => {
  const outline = { nodes: [{ id: 'n1', type: 'agent', label: 'Review', call: 'x.py:fn@x.py:1' }], errors: [] } as never
  const lines = runStepLines([], outline, [step('p', 'Fetch', 'x.py')])
  expect(lines.map(line => [line.title, line.kind])).toEqual([['Review', 'agent']])
})
