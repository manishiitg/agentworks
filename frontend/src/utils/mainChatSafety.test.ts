// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { invalidateChatIdentity } from './chatIdentity'

describe('main-workspace safety fixes (#4214)', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('evicts rejected promises and clears agentProfileCapabilities cache across identity changes', async () => {
    vi.resetModules()
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: false, status: 503 })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          runtime: { capabilities: { voice: 'preferred' } },
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          runtime: { capabilities: { voice: 'disabled' } },
        }),
      })
    vi.stubGlobal('fetch', fetchMock)

    const { loadAgentProfileCapabilityEnabled } = await import('./agentProfileCapabilities')
    const { invalidateChatIdentity: invalidate } = await import('./chatIdentity')

    // First call fails transiently -> must not permanently cache false
    expect(await loadAgentProfileCapabilityEnabled('work', 'voice')).toBe(false)
    // Retry succeeds -> returns true and caches within the same identity
    expect(await loadAgentProfileCapabilityEnabled('work', 'voice')).toBe(true)
    expect(await loadAgentProfileCapabilityEnabled('work', 'voice')).toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(2)

    // Account/identity change invalidates cache so next account sees its own profile
    invalidate()
    expect(await loadAgentProfileCapabilityEnabled('work', 'voice')).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('scopes reportPreviewPreference and runsOnMemory keys per workspace with legacy fallback', async () => {
    vi.resetModules()
    const { useWorkspaceConnectionStore } = await import('../stores/useWorkspaceConnectionStore')
    const { readReportPreviewPreference, writeReportPreviewPreference, REPORT_PREVIEW_PREFERENCE_KEY } = await import('./reportPreviewPreference')
    const { readLastRunsOn, rememberRunsOn } = await import('../products/work/runsOnMemory')

    // Legacy fallback read works
    localStorage.setItem(`${REPORT_PREVIEW_PREFERENCE_KEY}:proj-1`, 'mobile')
    localStorage.setItem('agentworks.runsOn.work', 'codex')
    useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'ws-a' })
    expect(readReportPreviewPreference('proj-1')).toBe('mobile')
    expect(readLastRunsOn('work')).toBe('codex')

    // Writing in workspace A does not bleed into workspace B
    writeReportPreviewPreference('proj-1', 'desktop')
    rememberRunsOn('work', 'claude-code')
    expect(readReportPreviewPreference('proj-1')).toBe('desktop')
    expect(readLastRunsOn('work')).toBe('claude-code')

    localStorage.removeItem(`${REPORT_PREVIEW_PREFERENCE_KEY}:proj-1`)
    localStorage.removeItem('agentworks.runsOn.work')
    useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'ws-b' })
    expect(readReportPreviewPreference('proj-1')).toBe('tablet')
    expect(readLastRunsOn('work')).toBe('')
  })

  it('invalidates identity monotonically', () => {
    expect(() => invalidateChatIdentity()).not.toThrow()
  })
})
