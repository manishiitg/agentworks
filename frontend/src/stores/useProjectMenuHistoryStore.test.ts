// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { createJSONStorage } from 'zustand/middleware'
import { projectMenuHistoryScope, useProjectMenuHistoryStore } from './useProjectMenuHistoryStore'

afterEach(() => useProjectMenuHistoryStore.setState({ recent: {} }))

it('keeps ten unique destinations in access order without growing on revisits', () => {
  const { visit } = useProjectMenuHistoryStore.getState()
  for (let i = 0; i < 12; i++) visit('crew', 'view-' + i)
  expect(useProjectMenuHistoryStore.getState().recent.crew).toEqual(Array.from({ length: 10 }, (_, i) => 'view-' + (11 - i)))
  visit('crew', 'view-5'); visit('crew', 'view-5')
  expect(useProjectMenuHistoryStore.getState().recent.crew).toHaveLength(10)
  expect(useProjectMenuHistoryStore.getState().recent.crew[0]).toBe('view-5')
})

it('restores histories without mixing accounts, servers, products or projects', async () => {
  const storage = new Map<string, string>()
  useProjectMenuHistoryStore.persist.setOptions({ storage: createJSONStorage(() => ({
    getItem: key => storage.get(key) ?? null,
    setItem: (key, value) => { storage.set(key, value) },
    removeItem: key => { storage.delete(key) },
  })) })
  const alice = projectMenuHistoryScope('local', 'alice', 'work', 'one')
  useProjectMenuHistoryStore.getState().visit(alice, 'files')
  const saved = storage.get('project-menu-history-v1')!
  useProjectMenuHistoryStore.setState({ recent: {} })
  storage.set('project-menu-history-v1', saved)
  await useProjectMenuHistoryStore.persist.rehydrate()
  expect(useProjectMenuHistoryStore.getState().recent[alice]).toEqual(['files'])
  for (const key of [projectMenuHistoryScope('local', 'bob', 'work', 'one'), projectMenuHistoryScope('remote', 'alice', 'work', 'one'), projectMenuHistoryScope('local', 'alice', 'work', 'two'), projectMenuHistoryScope('local', 'alice', 'relays', 'one')]) expect(useProjectMenuHistoryStore.getState().recent[key]).toBeUndefined()
})
