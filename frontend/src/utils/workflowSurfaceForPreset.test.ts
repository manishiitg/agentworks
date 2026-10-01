import { beforeEach, describe, expect, it } from 'vitest'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { workflowSurfaceForPreset } from './workflowNavigation'

describe('workflowSurfaceForPreset', () => {
  beforeEach(() => {
    ;(window as unknown as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'relays'] }
    useGlobalPresetStore.setState({
      workflowPresets: [
        { id: 'r1', workflowKind: 'relay' },
        { id: 'g1', workflowKind: 'workflow' },
      ] as never,
    })
  })

  it('opens a Relay in Relays and everything else in Goals', () => {
    expect(workflowSurfaceForPreset('r1')).toBe('relays')
    expect(workflowSurfaceForPreset('g1')).toBe('agentworks')
    expect(workflowSurfaceForPreset('unknown')).toBe('agentworks')
    expect(workflowSurfaceForPreset(undefined)).toBe('agentworks')
  })
})
