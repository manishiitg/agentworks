// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: { get: (...args: unknown[]) => get(...args) } }))
vi.mock('../../components/workflow/RelaySourceGraph', () => ({
  RelaySourceGraph: ({ graph }: { graph: { nodes: unknown[]; errors: string[] } }) => <div data-testid="graph">{graph.nodes.length} nodes {graph.errors.join(' ')}</div>,
}))
import { CrewScriptPlan } from './CrewScriptPlan'

let container: HTMLDivElement
let root: Root
async function render() {
  await act(async () => { root.render(<CrewScriptPlan workspacePath="Crew/example/code" onCode={() => {}} onAsk={() => {}} code={<p>the code</p>} />) })
  await act(async () => { await Promise.resolve() })
}

describe('CrewScriptPlan', () => {
  beforeEach(() => {
    ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
    container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); get.mockReset()
  })
  afterEach(() => { act(() => root.unmount()); container.remove() })

  it('draws the server outline when the script has steps', async () => {
    get.mockResolvedValue({ native: true, nodes: [{ id: 'step-1' }, { id: 'other-9' }], edges: [], errors: [] })
    await render()
    expect(container.querySelector('[data-testid="graph"]')?.textContent).toContain('2 nodes')
    expect(container.textContent).not.toContain('the code')
  })

  it('shows the server\'s readable message instead of a broken plan when the source is too large to outline', async () => {
    get.mockResolvedValue({ native: true, nodes: [], edges: [], errors: ['This script is too large to outline. Open the code.'] })
    await render()
    expect(container.querySelector('[data-testid="graph"]')?.textContent).toContain('too large to outline')
  })

  it('shows the code when there is no outline or the server could not read it', async () => {
    get.mockResolvedValue({ native: true, nodes: [], edges: [], errors: [] })
    await render()
    expect(container.textContent).toContain('the code')
    get.mockRejectedValue(new Error('no'))
    await act(async () => { root.render(<CrewScriptPlan workspacePath="Crew/other/code" onCode={() => {}} onAsk={() => {}} code={<p>the code</p>} />) })
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('the code')
  })
})
