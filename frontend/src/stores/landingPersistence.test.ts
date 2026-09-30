// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'

vi.mock('../services/api', () => ({ agentApi: {}, default: {}, getApiBaseUrl: () => '' }))
vi.mock('../services/llm-config-api', () => ({ llmConfigService: {} }))
import { useAppStore } from './useAppStore'
import { useLLMStore } from './useLLMStore'

// People land on their product after a reload or a sign-in, never on the last
// global page (Providers, Activity, Schedules).
describe('landing after a reload', () => {
  it('does not persist the Providers page as open', () => {
    const persisted = useLLMStore.persist.getOptions().partialize?.({ ...useLLMStore.getState(), showLLMModal: true }) as Record<string, unknown>
    expect(persisted).not.toHaveProperty('showLLMModal')
  })

  it('does not persist the Activity or Schedules pages', () => {
    const persisted = useAppStore.persist.getOptions().partialize?.(useAppStore.getState()) as Record<string, unknown>
    expect(persisted).not.toHaveProperty('showWorkflowsOverview')
    expect(persisted).not.toHaveProperty('showSchedulesOverview')
  })

  it('clears a global page that an older build persisted', () => {
    const migrate = useAppStore.persist.getOptions().migrate!
    const migrated = migrate({ showWorkflowsOverview: true, showSchedulesOverview: true }, 7) as Record<string, unknown>
    expect(migrated.showWorkflowsOverview).toBe(false)
    expect(migrated.showSchedulesOverview).toBe(false)
  })
})
