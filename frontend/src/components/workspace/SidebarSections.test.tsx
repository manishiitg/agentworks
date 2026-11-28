// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { sidebarProjectShortcuts } from '../../utils/sidebarProjectOrder'

afterEach(() => {
  useProjectMenuHistoryStore.setState({ recent: {}, startHere: {}, pinned: {}, sections: {} })
})

it('creates, renames, collapses, reorders, assigns projects, and deletes sidebar sections without duplicating in Pinned or Recent', () => {
  const scope = 'test-scope'
  const store = useProjectMenuHistoryStore.getState()
  store.setPinned(scope, 'p1', true)
  store.visit(scope, 'p2')
  store.visit(scope, 'p3')

  const clientsId = store.createSection(scope, 'Clients')
  const internalId = store.createSection(scope, 'Internal')
  expect(useProjectMenuHistoryStore.getState().sections[scope].map(s => s.name)).toEqual(['Clients', 'Internal'])

  // Assigning a pinned project to a section unpins it so it is never duplicated.
  store.assignProjectToSection(scope, 'p1', clientsId)
  store.assignProjectToSection(scope, 'p2', clientsId)
  expect(useProjectMenuHistoryStore.getState().pinned[scope]).toEqual([])
  expect(useProjectMenuHistoryStore.getState().sections[scope][0].items).toEqual(['p1', 'p2'])

  // Recent excludes sectioned projects.
  const rows = [{ id: 'p1' }, { id: 'p2' }, { id: 'p3' }]
  const sectioned = new Set(useProjectMenuHistoryStore.getState().sections[scope].flatMap(s => s.items))
  const { pinnedRows, recentRows } = sidebarProjectShortcuts(
    rows,
    r => r.id,
    useProjectMenuHistoryStore.getState().pinned[scope] || [],
    useProjectMenuHistoryStore.getState().recent[scope] || [],
    undefined,
    sectioned,
  )
  expect(pinnedRows).toEqual([])
  expect(recentRows.map(r => r.id)).toEqual(['p3'])

  // Collapse, rename, and reorder sections.
  store.toggleSectionCollapsed(scope, clientsId)
  expect(useProjectMenuHistoryStore.getState().sections[scope][0].collapsed).toBe(true)
  store.renameSection(scope, clientsId, 'Client work')
  expect(useProjectMenuHistoryStore.getState().sections[scope][0].name).toBe('Client work')
  store.moveSection(scope, internalId, -1)
  expect(useProjectMenuHistoryStore.getState().sections[scope].map(s => s.name)).toEqual(['Internal', 'Client work'])

  // Pinning a sectioned project moves it out of its section.
  store.setPinned(scope, 'p1', true)
  expect(useProjectMenuHistoryStore.getState().pinned[scope]).toEqual(['p1'])
  expect(useProjectMenuHistoryStore.getState().sections[scope].find(s => s.id === clientsId)?.items).toEqual(['p2'])

  // Deleting a section removes only the section; recent history remains intact.
  store.deleteSection(scope, clientsId)
  expect(useProjectMenuHistoryStore.getState().sections[scope].map(s => s.id)).toEqual([internalId])
  expect(useProjectMenuHistoryStore.getState().recent[scope]).toContain('p2')
})
