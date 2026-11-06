// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../stores/useGlobalPresetStore', () => ({
  useGlobalPresetStore: {
    getState: () => ({
      workflowPresets: [
        { id: 'r1', workflowKind: 'relay' },
        { id: 'g1', workflowKind: 'workflow' },
      ],
    }),
  },
}))

import { workflowSurfaceForPreset } from './workflowNavigation'

describe('workflowSurfaceForPreset', () => {
  beforeEach(() => {
    ;(window as unknown as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'relays'] }
  })

  it('does not navigate saved Relay presets into the disabled product', () => {
    expect(workflowSurfaceForPreset('r1')).toBe('agentworks')
    expect(workflowSurfaceForPreset('g1')).toBe('agentworks')
    expect(workflowSurfaceForPreset('unknown')).toBe('agentworks')
    expect(workflowSurfaceForPreset(undefined)).toBe('agentworks')
  })
})
