import { expect, it } from 'vitest'
import { groupWorkspacePanels, panelSearchAliases } from './workspacePanelGroups'
import { workWorkspacePanels } from '../../products/work/workWorkspacePanels'
import { workflowContextViews } from '../workflow/workspaceViews'

it.each(['work', 'code', 'agentworks', 'relays'] as const)('preserves every admitted %s panel when grouping the menu', product => {
  const work = workWorkspacePanels({ profileId: product === 'code' ? 'code' : 'work', showShell: true })
  const panels = product === 'work' || product === 'code'
    ? [...work.views, ...work.ops, ...work.setup]
    : workflowContextViews(product === 'relays', false)
  const grouped = groupWorkspacePanels<{ id: string }>(panels).flatMap(group => group.items.map(item => item.id))
  expect(grouped.sort()).toEqual(panels.map(item => item.id).sort())
})

it('cannot introduce setup or terminal access into a run-only Crew menu', () => {
  const work = workWorkspacePanels({ readOnly: true, showShell: true })
  const groups = groupWorkspacePanels([...work.views, ...work.ops, ...work.setup])
  expect(groups.flatMap(group => group.items.map(item => item.id))).toEqual(['dashboard'])
})

it('keeps old panel terminology searchable and retains unknown admitted panels', () => {
  expect(panelSearchAliases.mcp).toContain('Integrations')
  expect(panelSearchAliases.identity).toContain('Identity')
  expect(groupWorkspacePanels([{ id: 'future-tool' }])).toEqual([{ title: 'Other tools', items: [{ id: 'future-tool' }] }])
})
