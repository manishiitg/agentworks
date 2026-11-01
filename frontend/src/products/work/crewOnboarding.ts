import { useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { crewStarterShortcuts, type CrewOnboarding } from './crewStarterTypes'

/** Suggested shortcuts only, after creation succeeds. No access changes. */
export async function prepareCrewOnboarding({ scope, onboarding, isCurrent }: {
  scope: string
  onboarding: CrewOnboarding
  isCurrent: () => boolean
}): Promise<'menu' | 'stale'> {
  if (!isCurrent()) return 'stale'
  useProjectMenuHistoryStore.getState().seedStartHere(scope, crewStarterShortcuts(onboarding.type))
  return 'menu'
}
