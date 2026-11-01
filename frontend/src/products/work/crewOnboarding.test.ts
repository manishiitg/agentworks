// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { prepareCrewOnboarding } from './crewOnboarding'
import { useProjectMenuHistoryStore, projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
const scope = projectMenuHistoryScope('local', 'person', 'work', 'new-crew')
afterEach(() => { useProjectMenuHistoryStore.setState({ recent: {}, startHere: {} }) })
it('seeds only the created scope without granting access or marking pages visited', async () => {
  expect(await prepareCrewOnboarding({ scope, isCurrent: () => true, onboarding: { type: 'automation' } })).toBe('menu')
  expect(useProjectMenuHistoryStore.getState().startHere[scope]).toEqual(['schedules:functions', 'memory:skills', 'schedules:schedules', 'schedules:conditions', 'schedules:triggers'])
  expect(useProjectMenuHistoryStore.getState().recent).toEqual({})
  useProjectMenuHistoryStore.setState({ startHere: {} })
  expect(await prepareCrewOnboarding({ scope, isCurrent: () => false, onboarding: { type: 'code' } })).toBe('stale')
  expect(useProjectMenuHistoryStore.getState().startHere).toEqual({})
})
