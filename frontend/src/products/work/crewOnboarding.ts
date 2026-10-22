import api from '../../services/api'
import { useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { crewStarterShortcuts, type CrewOnboarding } from './crewStarterTypes'

/** Called only after creation succeeds. Sharing is a read-only admission check. */
export async function prepareCrewOnboarding({ scope, workspacePath, onboarding, isCurrent }: {
  scope: string
  workspacePath: string
  onboarding: CrewOnboarding
  isCurrent: () => boolean
}): Promise<'menu' | 'sharing' | 'sharing-unavailable' | 'stale'> {
  if (!isCurrent()) return 'stale'
  useProjectMenuHistoryStore.getState().seedStartHere(scope, crewStarterShortcuts(onboarding.type))
  if (!onboarding.openSharing) return 'menu'
  try {
    const { data } = await api.post<{ can_share: boolean }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }, { timeout: 10000 })
    if (!isCurrent()) return 'stale'
    return data.can_share === true ? 'sharing' : 'sharing-unavailable'
  } catch {
    return isCurrent() ? 'sharing-unavailable' : 'stale'
  }
}
