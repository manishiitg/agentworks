import { useEffect } from 'react'
import { create } from 'zustand'
import { liveFeed } from '../../services/liveFeed'
import { llmConfigService, type MenuFilter as SavedMenuFilter } from '../../services/llm-config-api'

// The Projects page choice: All, Mine or Shared with me, and the open location tab (All, this Mac or a network). The
// server keeps the last choice for the person (menu_filter.go), so it survives a restart and is the same on every
// screen (owner, 10 Nov). Project groups are gone (owner, 22 Nov); the Filters button became tabs (owner, 24 Nov).

export type MenuFilter = 'all' | 'mine' | 'shared'

type Saved = { filter: MenuFilter; network: string }
const useMenuFilterStore = create<Saved & { loaded: boolean; error: string; set: (saved: Partial<Saved>) => void; fail: (error: string) => void }>()(set => ({
  filter: 'all', network: '', loaded: false, error: '',
  set: saved => set({ ...saved, loaded: true, error: '' }),
  fail: error => set({ error }),
}))

const fromServer = (result: SavedMenuFilter): Saved => ({ filter: result.filter === 'mine' || result.filter === 'shared' ? result.filter : 'all', network: result.network || '' })

let loading: Promise<void> | null = null
function loadFilter() {
  loading ??= llmConfigService.getMenuFilter()
    .then(result => useMenuFilterStore.getState().set(fromServer(result)))
    .catch(() => useMenuFilterStore.getState().fail('Could not load your filter.'))
    .finally(() => { loading = null })
  return loading
}
/** Saves a choice: the screen shows it at once and then what the server kept. */
async function saveFilter(change: Partial<Saved>) {
  const store = useMenuFilterStore.getState()
  const next = { filter: store.filter, network: store.network, ...change }
  store.set(next)
  try {
    useMenuFilterStore.getState().set(fromServer(await llmConfigService.setMenuFilter(next)))
  } catch {
    useMenuFilterStore.getState().fail('Could not save your filter. Try again.')
  }
}

export function useMenuFilter(enabled = true) {
  const state = useMenuFilterStore()
  useEffect(() => enabled ? liveFeed.subscribe(['menu_filter'], null, () => { void loadFilter() }) : undefined, [enabled])
  useEffect(() => { if (enabled && !state.loaded) void loadFilter() }, [enabled, state.loaded])
  return { error: state.error, filter: state.filter, network: state.network, pick: (next: MenuFilter, network?: string) => void saveFilter({ filter: next, ...(network === undefined ? {} : { network }) }) }
}

/** A project shared with the person: a Team copy on this Mac, or someone else's Crew on a server. */
export function isSharedProject(project: { owned_by_caller?: boolean; shared_line?: string }) {
  return project.owned_by_caller === false || Boolean(project.shared_line)
}

export function matchesMenuFilter(filter: MenuFilter, project: { owned_by_caller?: boolean; shared_line?: string }) {
  if (filter === 'mine') return !isSharedProject(project)
  if (filter === 'shared') return isSharedProject(project)
  return true
}
