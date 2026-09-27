import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  deploymentDefaultProductSurface,
  enabledProductSurfaces,
  intersectAllowedProductSurfaces,
  isEnabledProductSurface,
  hasGatewaySSO,
  isSingleProductDeployment,
} from './productSurfaceConfig'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('product surface deployment configuration', () => {
  it('defaults to AgentWorks, Relays, and Crew when no deployment allowlist is configured', () => {
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays', 'work'])
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
    expect(isSingleProductDeployment()).toBe(false)
    expect(hasGatewaySSO()).toBe(false)
  })

  it('constrains the dedicated host to AgentWorks and Video Studio', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'video-studio',
        enabledProductSurfaces: ['agentworks', 'video-studio'],
      },
    })

    expect(enabledProductSurfaces()).toEqual(['agentworks', 'video-studio'])
    expect(deploymentDefaultProductSurface()).toBe('video-studio')
    expect(isEnabledProductSurface('agentworks')).toBe(true)
    expect(isEnabledProductSurface('dominion')).toBe(false)
    expect(isSingleProductDeployment()).toBe(false)
  })

  it('lets the SparkQuill desktop pin itself to the one surface it ships', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'sparkquill',
        enabledProductSurfaces: ['sparkquill'],
        gatewaySso: true,
      },
    })

    expect(enabledProductSurfaces()).toEqual(['sparkquill'])
    expect(deploymentDefaultProductSurface()).toBe('sparkquill')
    expect(isEnabledProductSurface('agentworks')).toBe(false)
    expect(isSingleProductDeployment()).toBe(true)
    expect(hasGatewaySSO()).toBe(true)
  })

  it('exposes the Work surface when the deployment allowlists it', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'agentworks',
        enabledProductSurfaces: ['agentworks', 'work'],
      },
    })

    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work'])
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
    expect(isEnabledProductSurface('work')).toBe(true)
    expect(isEnabledProductSurface('dominion')).toBe(false)
    expect(isSingleProductDeployment()).toBe(false)
  })
})

describe('intersectAllowedProductSurfaces', () => {
  it('passes the deployment list through unchanged when the user is unrestricted', () => {
    expect(intersectAllowedProductSurfaces(['dominion', 'agentworks'], null)).toEqual(['dominion', 'agentworks'])
    expect(intersectAllowedProductSurfaces(['dominion', 'agentworks'], undefined)).toEqual(['dominion', 'agentworks'])
    // An empty list is a read-only account with nothing enabled: no products.
    expect(intersectAllowedProductSurfaces(['dominion', 'agentworks'], [])).toEqual([])
  })

  it('narrows to the explicit per-user allowlist, case-insensitively', () => {
    expect(intersectAllowedProductSurfaces(['dominion', 'agentworks'], ['Dominion'])).toEqual(['dominion'])
    expect(intersectAllowedProductSurfaces(['agentworks', 'relays', 'work'], ['agentworks'])).toEqual(['agentworks', 'relays'])
  })
})
