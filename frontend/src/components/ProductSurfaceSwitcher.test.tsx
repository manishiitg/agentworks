import { afterEach, describe, expect, it, vi } from 'vitest'

// ProductSurfaceSwitcher.tsx reads the logged-in user's allowed_products from
// useAuthStore, which pulls in services/api.ts -- a module with an eager,
// order-dependent top-level side effect (mcpConfigApi's constructor calls
// getApiBaseUrl() at import time) that only this test file's module graph
// reaches. Mocking the store here, the same way other tests in this repo
// mock services/api directly, keeps the test isolated from that unrelated
// fragility rather than trying to fix it as part of an unrelated feature.
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: () => undefined }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({}) } }))

import { visibleProductSurfaceIDs } from './ProductSurfaceSwitcher'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ProductSurfaceSwitcher deployment allowlist', () => {
  it('shows AgentWorks, Relays, and Crew in the local product switcher', () => {
    expect(visibleProductSurfaceIDs()).toEqual(['agentworks', 'relays', 'work'])
  })
  it('shows only AgentWorks and Video Studio on the dedicated server', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'video-studio',
        enabledProductSurfaces: ['agentworks', 'video-studio'],
      },
    })
    expect(visibleProductSurfaceIDs()).toEqual(['agentworks', 'video-studio'])
  })

  it('further narrows to a per-user allowlist on top of the deployment allowlist', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'dominion',
        enabledProductSurfaces: ['dominion', 'agentworks'],
      },
    })
    expect(visibleProductSurfaceIDs(['dominion'])).toEqual(['dominion'])
    expect(visibleProductSurfaceIDs(['dominion', 'agentworks'])).toEqual(['agentworks', 'dominion'])
    expect(visibleProductSurfaceIDs(null)).toEqual(['agentworks', 'dominion'])
  })

  it('shows Work alongside AgentWorks when the deployment allowlists it', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'agentworks',
        enabledProductSurfaces: ['agentworks', 'work'],
      },
    })
    expect(visibleProductSurfaceIDs(null)).toEqual(['agentworks', 'work'])
    expect(visibleProductSurfaceIDs(['work'])).toEqual(['work'])
  })
})
