import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  deploymentDefaultProductSurface,
  enabledProductSurfaces,
  gatewayAdminUrl,
  gatewayBaseUrl,
  intersectAllowedProductSurfaces,
  isProductSurface,
  isEnabledProductSurface,
  hasGatewaySSO,
  isSingleProductDeployment,
} from './productSurfaceConfig'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

describe('product surface deployment configuration', () => {
  it('excludes shared server products from local installations despite stale configuration', () => {
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: {
      deploymentMode: 'local',
      enabledProductSurfaces: ['agentworks', 'relays', 'work', 'code', 'knowledgebase', 'mcp-gateway'],
      defaultProductSurface: 'mcp-gateway', gatewayUrl: 'http://127.0.0.1:18745', gatewaySso: true,
    } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work'])
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
    expect(gatewayBaseUrl()).toBeNull()
    expect(hasGatewaySSO()).toBe(false)
    for (const product of ['code', 'relays', 'knowledgebase', 'mcp-gateway'] as const) expect(isEnabledProductSurface(product)).toBe(false)
  })

  it('uses local defaults and cannot opt into server products in a local build', () => {
    vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { deploymentMode: 'server', enabledProductSurfaces: ['code', 'mcp-gateway', 'knowledgebase', 'relays'], gatewayUrl: 'http://127.0.0.1:18745' } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work'])
    expect(gatewayBaseUrl()).toBeNull()
  })

  it('enables server products for an explicit source-development opt-in', () => {
    vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local-full')
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: {
      deploymentMode: 'local', localServerProducts: true,
      enabledProductSurfaces: ['agentworks', 'relays', 'work', 'code', 'knowledgebase', 'mcp-gateway'],
      gatewayUrl: 'http://127.0.0.1:18745',
    } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays', 'work', 'code', 'knowledgebase', 'mcp-gateway'])
    expect(gatewayBaseUrl()).toBe('http://127.0.0.1:18745')
  })

  it('does not enable missing server assets in a packaged local build', () => {
    vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: {
      deploymentMode: 'local', localServerProducts: true,
      enabledProductSurfaces: ['agentworks', 'relays', 'work', 'code', 'knowledgebase', 'mcp-gateway'],
      gatewayUrl: 'http://127.0.0.1:18745',
    } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work'])
    expect(gatewayBaseUrl()).toBeNull()
  })
  it('ignores a retired product in saved deployment configuration', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'dominion',
        enabledProductSurfaces: ['dominion', 'agentworks'],
      },
    })
    expect(isProductSurface('dominion')).toBe(false)
    expect(enabledProductSurfaces()).toEqual(['agentworks'])
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
  })

  it('defaults to AgentWorks, Relays, Crew, and Brain when no deployment allowlist is configured', () => {
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays', 'work', 'knowledgebase'])
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
    expect(isSingleProductDeployment()).toBe(false)
    expect(hasGatewaySSO()).toBe(false)
  })

  it('exposes Vault when a shared gateway URL is configured', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: { gatewayUrl: 'http://127.0.0.1:18745' },
    })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays', 'work', 'mcp-gateway', 'knowledgebase'])
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
    expect(isEnabledProductSurface('sparkquill')).toBe(false)
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
    expect(isEnabledProductSurface('sparkquill')).toBe(false)
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
  it('preserves Brain reader entitlement without granting other products', () => {
    expect(intersectAllowedProductSurfaces(['agentworks', 'work', 'knowledgebase'], ['knowledgebase'])).toEqual(['knowledgebase'])
    expect(intersectAllowedProductSurfaces(['knowledgebase'], [])).toEqual([])
  })
  it('passes the deployment list through unchanged when the user is unrestricted', () => {
    expect(intersectAllowedProductSurfaces(['video-studio', 'agentworks'], null)).toEqual(['video-studio', 'agentworks'])
    expect(intersectAllowedProductSurfaces(['video-studio', 'agentworks'], undefined)).toEqual(['video-studio', 'agentworks'])
    // An empty list is a read-only account with nothing enabled: no products.
    expect(intersectAllowedProductSurfaces(['video-studio', 'agentworks'], [])).toEqual([])
  })

  it('narrows to the explicit per-user allowlist, case-insensitively', () => {
    expect(intersectAllowedProductSurfaces(['video-studio', 'agentworks'], ['Video-Studio'])).toEqual(['video-studio'])
    expect(intersectAllowedProductSurfaces(['agentworks', 'relays', 'work'], ['agentworks'])).toEqual(['agentworks'])
    expect(intersectAllowedProductSurfaces(['agentworks', 'relays', 'work'], ['relays'])).toEqual(['relays'])
  })
})
