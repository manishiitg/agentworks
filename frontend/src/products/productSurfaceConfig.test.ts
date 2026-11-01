import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  PRODUCT_SURFACE_LABELS,
  navigationProductSurfaceIDs,
  visibleProductSurfaceIDs,
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
      enabledProductSurfaces: ['agentworks', 'relays', 'work', 'code', 'knowledgebase', 'mcp-gateway', 'llm-gateway'],
      defaultProductSurface: 'knowledgebase', gatewayUrl: 'http://127.0.0.1:18745', gatewaySso: true,
    } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work'])
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
    expect(gatewayBaseUrl()).toBeNull()
    expect(hasGatewaySSO()).toBe(false)
    for (const product of ['relays', 'knowledgebase', 'mcp-gateway', 'llm-gateway'] as const) expect(isEnabledProductSurface(product)).toBe(false)
  })

  it('uses local defaults and cannot opt into server products in a local build', () => {
    vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { deploymentMode: 'server', enabledProductSurfaces: ['mcp-gateway', 'knowledgebase', 'relays'], gatewayUrl: 'http://127.0.0.1:18745' } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'work', 'bullpen'])
    expect(gatewayBaseUrl()).toBeNull()
  })

  it('enables server products for an explicit source-development opt-in', () => {
    vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local-full')
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: {
      deploymentMode: 'local', localServerProducts: true,
      enabledProductSurfaces: ['agentworks', 'relays', 'work', 'code', 'knowledgebase', 'mcp-gateway'],
      gatewayUrl: 'http://127.0.0.1:18745',
    } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays', 'work', 'knowledgebase', 'mcp-gateway'])
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

  it('defaults to AgentWorks, Flows, Crew, and Brain when no deployment allowlist is configured', () => {
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays', 'work', 'knowledgebase'])
    expect(PRODUCT_SURFACE_LABELS.relays).toBe('Flows')
    expect(deploymentDefaultProductSurface()).toBe('agentworks')
    expect(isSingleProductDeployment()).toBe(false)
    expect(enabledProductSurfaces()).not.toContain('bullpen')
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

  it('constrains the dedicated host to AgentWorks and Relays', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'relays',
        enabledProductSurfaces: ['agentworks', 'relays'],
      },
    })

    expect(enabledProductSurfaces()).toEqual(['agentworks', 'relays'])
    expect(deploymentDefaultProductSurface()).toBe('relays')
    expect(isEnabledProductSurface('agentworks')).toBe(true)
    expect(isEnabledProductSurface('knowledgebase')).toBe(false)
    expect(isSingleProductDeployment()).toBe(false)
  })

  it('pins a dedicated host to its one enabled surface', () => {
    vi.stubGlobal('window', {
      __APP_RUNTIME_CONFIG__: {
        defaultProductSurface: 'relays',
        enabledProductSurfaces: ['relays'],
        gatewaySso: true,
      },
    })

    expect(enabledProductSurfaces()).toEqual(['relays'])
    expect(deploymentDefaultProductSurface()).toBe('relays')
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
    expect(isEnabledProductSurface('knowledgebase')).toBe(false)
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
    expect(intersectAllowedProductSurfaces(['relays', 'agentworks'], null)).toEqual(['relays', 'agentworks'])
    expect(intersectAllowedProductSurfaces(['relays', 'agentworks'], undefined)).toEqual(['relays', 'agentworks'])
    // An empty list is a read-only account with nothing enabled: no products.
    expect(intersectAllowedProductSurfaces(['relays', 'agentworks'], [])).toEqual([])
  })

  it('narrows to the explicit per-user allowlist, case-insensitively', () => {
    expect(intersectAllowedProductSurfaces(['relays', 'agentworks'], ['Relays'])).toEqual(['relays'])
    expect(intersectAllowedProductSurfaces(['agentworks', 'relays', 'work'], ['agentworks'])).toEqual(['agentworks'])
    expect(intersectAllowedProductSurfaces(['agentworks', 'relays', 'work'], ['relays'])).toEqual(['relays'])
  })
})

 it('uses the same-origin LLM surface without a gateway URL', () => {
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { deploymentMode: 'server', gatewayUrl: 'http://127.0.0.1:18745' } })
    expect(enabledProductSurfaces()).not.toContain('llm-gateway')
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'llm-gateway'], gatewayUrl: 'http://127.0.0.1:18745' } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'llm-gateway'])
    vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'llm-gateway'] } })
    expect(enabledProductSurfaces()).toEqual(['agentworks', 'llm-gateway'])
  })

it('allows an explicitly configured same-origin LLM default on a managed server', () => {
 vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { deploymentMode: 'server', enabledProductSurfaces: ['llm-gateway', 'work'], defaultProductSurface:'llm-gateway' } })
 expect(enabledProductSurfaces()).toEqual(['llm-gateway','work'])
 expect(deploymentDefaultProductSurface()).toBe('llm-gateway')
})

// Retired product grants do not grant access to a current Crew.
it.each([
  [['work', 'code'], ['work']],
  [['code'], []],
  [['work'], ['work']],
  [[], []],
])('filters retired products without widening %j', (allowed, navigation) => {
  vi.stubGlobal('window', { __APP_RUNTIME_CONFIG__: { enabledProductSurfaces: ['work', 'code'] } })
  expect(visibleProductSurfaceIDs(allowed)).toEqual(navigation)
  expect(navigationProductSurfaceIDs(allowed)).toEqual(navigation)
})
