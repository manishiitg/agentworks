import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  deploymentDefaultProductSurface,
  enabledProductSurfaces,
  gatewayAdminUrl,
  gatewayBaseUrl,
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

  it('opts into CapLayer when a gateway URL is configured', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'http://127.0.0.1:18745' },
    })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work', 'mcp-gateway'])
  })

  it('removes CapLayer from an explicit allowlist when its endpoint is withdrawn', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { enabledProductSurfaces: ['agentworks', 'mcp-gateway'] },
    })
    expect(enabledProductSurfaces()).toEqual(['agentworks'])
    expect(isEnabledProductSurface('mcp-gateway')).toBe(false)
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

describe('gatewayAdminUrl', () => {
  it('is null when no gateway is configured', () => {
    expect(gatewayAdminUrl()).toBeNull()
  })

  it('links to the configured gateway admin', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'https://mcp.agentworkshq.com/' },
    })
    expect(gatewayAdminUrl()).toBe('https://mcp.agentworkshq.com/admin/')
  })

  it('accepts loopback http for local development', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'http://127.0.0.1:18080' },
    })
    expect(gatewayAdminUrl()).toBe('http://127.0.0.1:18080/admin/')
  })

  it('rejects non-http(s) values', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'javascript:alert(1)' },
    })
    expect(gatewayAdminUrl()).toBeNull()
  })
})

describe('gatewayBaseUrl', () => {
  it('is null when no gateway is configured', () => {
    expect(gatewayBaseUrl()).toBeNull()
  })

  it('returns the validated base without a trailing slash', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'http://127.0.0.1:18080/' },
    })
    expect(gatewayBaseUrl()).toBe('http://127.0.0.1:18080')
  })

  it('rejects non-http(s) values', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'javascript:alert(1)' },
    })
    expect(gatewayBaseUrl()).toBeNull()
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
