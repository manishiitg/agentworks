import { expect, it } from 'vitest'
import { workWorkspacePanels } from './workWorkspacePanels'
const ids = (result: ReturnType<typeof workWorkspacePanels>) =>
  [...result.views, ...result.ops, ...result.setup].map((item) => item.id)
it('preserves the merged Crew features for Code-origin owner projects', () => {
  const panels = ids(workWorkspacePanels({ profileId: 'work', showShell: true }))
  for (const wanted of ['dashboard', 'files', 'shell', 'browser', 'schedules', 'costs', 'identity', 'mcp'])
    expect(panels).toContain(wanted)
  expect(panels).toContain('database')
  expect(panels).toContain('suggestions')
  expect(ids(workWorkspacePanels({ profileId: 'work', showShell: false }))).not.toContain('shell')
})
it('preserves shared/read-only dashboard-only access and feature gates', () => {
  expect(ids(workWorkspacePanels({ readOnly: true, showShell: true }))).toEqual(['dashboard'])
  expect(ids(workWorkspacePanels({ readOnly: true, enabledPanels: new Set() }))).toEqual([])
  const panels = ids(workWorkspacePanels({ profileId: 'work', enabledPanels: new Set(['files', 'models']) }))
  expect(panels).toEqual(['suggestions', 'files', 'identity'])
})
