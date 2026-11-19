import { useEffect } from 'react'
import { create } from 'zustand'
import { agentApi } from '../../services/api'

/** The person's own left-menu groups of one Crew's agents (#3288): conversation key → group name, as the server saved
 * them. Layout only; the server decides names (a new group may not reuse one, a rename may not take another's). */
type Groups = Record<string, string>
type Edit = Parameters<typeof agentApi.editAgentGroups>[1]

export const useAgentGroupsStore = create<{ byProject: Record<string, Groups>; load: (projectId: string) => Promise<void>; edit: (projectId: string, edit: Edit) => Promise<void> }>(set => ({
  byProject: {},
  load: async projectId => {
    try {
      const groups = await agentApi.agentGroups(projectId)
      set(state => ({ byProject: { ...state.byProject, [projectId]: groups } }))
    } catch { /* The menu stays as it was; the next focus reads again. */ }
  },
  edit: async (projectId, edit) => {
    try {
      const groups = await agentApi.editAgentGroups(projectId, edit)
      set(state => ({ byProject: { ...state.byProject, [projectId]: groups } }))
    } catch (cause) {
      const message = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      throw new Error(message || 'Couldn’t save this group change. Try again.')
    }
  },
}))

const empty: Groups = {}

/** The groups of one Crew, read when the menu shows it and again when the window comes back (a chat may have changed them). */
export function useAgentGroups(projectId: string): Groups {
  const groups = useAgentGroupsStore(state => state.byProject[projectId]) ?? empty
  useEffect(() => {
    const load = () => { void useAgentGroupsStore.getState().load(projectId) }
    load()
    window.addEventListener('focus', load)
    return () => window.removeEventListener('focus', load)
  }, [projectId])
  return groups
}

/** Group names in the order the menu shows them. */
export function groupNames(groups: Groups): string[] {
  return [...new Set(Object.values(groups))].sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()))
}
