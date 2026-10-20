// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { createJSONStorage } from 'zustand/middleware'
import { sidebarExpansionKey, useSidebarExpansionStore } from './useSidebarExpansionStore'

afterEach(() => { useSidebarExpansionStore.setState({ expanded: {} }) })

it('defaults closed and restores project choices independently for each user and server', async () => {
  const storage = new Map<string, string>()
  useSidebarExpansionStore.persist.setOptions({ storage: createJSONStorage(() => ({
    getItem: key => storage.get(key) ?? null,
    setItem: (key, value) => { storage.set(key, value) },
    removeItem: key => { storage.delete(key) },
  })) })
  const alice = sidebarExpansionKey('local:alice', 'project', 'goal:one')
  const bob = sidebarExpansionKey('local:bob', 'project', 'goal:one')
  const remote = sidebarExpansionKey('remote:alice', 'project', 'goal:one')
  const otherProject = sidebarExpansionKey('local:alice', 'project', 'goal:two')
  expect(useSidebarExpansionStore.getState().expanded[alice]).toBeUndefined()
  useSidebarExpansionStore.getState().toggle(alice)
  const stored = storage.get('sidebar-expansion-v1')
  useSidebarExpansionStore.setState({ expanded: {} })
  storage.set('sidebar-expansion-v1', stored!)
  await useSidebarExpansionStore.persist.rehydrate()
  expect(useSidebarExpansionStore.getState().expanded[alice]).toBe(true)
  for (const key of [bob, remote, otherProject]) expect(useSidebarExpansionStore.getState().expanded[key]).toBeUndefined()
  useSidebarExpansionStore.getState().toggle(alice)
  await useSidebarExpansionStore.persist.rehydrate()
  expect(useSidebarExpansionStore.getState().expanded[alice]).toBe(false)
})
