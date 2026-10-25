// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { agentApi } from '../../services/api'
import { SoulViewer } from './SoulViewer'

vi.mock('../../services/api', () => ({ agentApi: { getBuilderDoc: vi.fn() } }))
vi.mock('../ui/MarkdownRenderer', () => ({ MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows primary goals first while preserving secondary goals and other commitments', async () => {
  vi.mocked(agentApi.getBuilderDoc).mockResolvedValue({ success: true, exists: true, doc: 'soul', path: 'soul/soul.md', content: `## Objective
Keep the agreed scope.
### Secondary goals
- Make the learner experience ready quickly.
### Primary goals
- Make conversations feel immediate.
## Success Criteria
- Meet agreed targets.
## Constraints
- Preserve privacy.` })
  const container = document.createElement('div'), root = createRoot(container)
  try {
    await act(async () => root.render(<SoulViewer workspacePath="Workflow/learner" goalSummary />))
    expect(Array.from(container.querySelectorAll('h3')).map(el => el.textContent)).toEqual(['Primary goals', 'Secondary goals', 'Additional goal context'])
    const text = container.textContent || ''
    expect(text.indexOf('Make conversations feel immediate.')).toBeLessThan(text.indexOf('Make the learner experience ready quickly.'))
    expect(text.match(/Make conversations feel immediate\./g)).toHaveLength(1)
    for (const content of ['Keep the agreed scope.', 'Meet agreed targets.', 'Preserve privacy.']) expect(text).toContain(content)
  } finally { await act(async () => root.unmount()) }
})

it('does not assign priorities to legacy goals', async () => {
  vi.mocked(agentApi.getBuilderDoc).mockResolvedValue({ success: true, exists: true, doc: 'soul', path: 'soul/soul.md', content: '## Objective\n- Grow the audience.\n- Retain readers.' })
  const container = document.createElement('div'), root = createRoot(container)
  try {
    await act(async () => root.render(<SoulViewer workspacePath="Workflow/social" goalSummary />))
    expect(container.textContent).toContain('Grow the audience.')
    expect(container.textContent).toContain('Retain readers.')
    expect(container.textContent).not.toContain('Primary goals')
    expect(container.textContent).not.toContain('Secondary goals')
  } finally { await act(async () => root.unmount()) }
})
