// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
const request = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { post: request } }))
import { prepareCrewOnboarding } from './crewOnboarding'
import { useProjectMenuHistoryStore, projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
const scope = projectMenuHistoryScope('local', 'alice', 'work', 'new-crew')
const base = { scope, workspacePath: 'Crew/new-crew', isCurrent: () => true }
afterEach(() => { useProjectMenuHistoryStore.setState({ recent: {}, startHere: {} }); vi.resetAllMocks() })
it('seeds onboarding only for the created scope, without marking pages visited or granting access', async () => {
  expect(await prepareCrewOnboarding({ ...base, onboarding: { type: 'automation', openSharing: false } })).toBe('menu')
  expect(useProjectMenuHistoryStore.getState().startHere[scope]).toEqual(['schedules:functions', 'memory:skills', 'schedules:schedules', 'schedules:conditions', 'schedules:triggers'])
  expect(useProjectMenuHistoryStore.getState().recent).toEqual({})
  expect(request).not.toHaveBeenCalled()
})
it('opens optional sharing only after the existing server admission, with no permission mutation', async () => {
  request.mockResolvedValue({ data: { can_share: true } })
  expect(await prepareCrewOnboarding({ ...base, onboarding: { type: 'specialist', openSharing: true } })).toBe('sharing')
  expect(request).toHaveBeenCalledExactlyOnceWith('/api/dashboards', { workspace: 'Crew/new-crew', action: 'share_status' }, { timeout: 10000 })
})
it.each([false, undefined])('does not open sharing when server admission is %s', async allowed => {
  request.mockResolvedValue({ data: { can_share: allowed } })
  expect(await prepareCrewOnboarding({ ...base, onboarding: { type: 'brain', openSharing: true } })).toBe('sharing-unavailable')
})
it('keeps successful creation on sharing errors and drops stale account/server responses', async () => {
  request.mockRejectedValue(new Error('offline'))
  expect(await prepareCrewOnboarding({ ...base, onboarding: { openSharing: true } })).toBe('sharing-unavailable')
  request.mockClear()
  expect(await prepareCrewOnboarding({ ...base, isCurrent: () => false, onboarding: { type: 'code', openSharing: true } })).toBe('stale')
  expect(request).not.toHaveBeenCalled()
  let current = true
  request.mockImplementation(async () => { current = false; return { data: { can_share: true } } })
  expect(await prepareCrewOnboarding({ ...base, isCurrent: () => current, onboarding: { openSharing: true } })).toBe('stale')
})
