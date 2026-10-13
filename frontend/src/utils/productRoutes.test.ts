import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { isLegacyAppPath, LEGACY_APP_ROOTS, legacyAppRoot, parseProductRoute, productReturnPath, productRoutePath, PRODUCT_PATHS, PRODUCT_ROUTE_ALIASES } from './productRoutes'

describe('shareable product routes', () => {
  it('round trips every surface and the resource/view contract', () => {
    const paths = [...Object.values(PRODUCT_PATHS), '/workflows', '/bullpen/connect', '/relays/example', '/relays/example/dashboard', '/workflows/example/dashboard', '/crew/projects/example?view=memory', '/code/projects/example/dashboard', '/video-studio/projects/example?view=production', '/brain?view=access&folder=Guides%2FGetting+started']
    for (const path of paths) {
      const route = parseProductRoute(path)
      expect(route, path).toBeTruthy()
      expect(productRoutePath(route!), path).toBe(path)
    }
    expect(productReturnPath('/work')).toBe('/crew')
    expect(productReturnPath('/knowledgebase')).toBe('/brain')
    expect(productReturnPath('/mcp-gateway')).toBe('/vault')
    expect(productReturnPath('/agentworks')).toBe('/goals')
    expect(productReturnPath('/agentworks/example')).toBe('/workflows/example')
    expect(productReturnPath('/code/projects/example?view=files')).toBe('/code/projects/example')
    expect(parseProductRoute('/')).toBeNull()
    expect(isLegacyAppPath('/settings')).toBe(true)
    expect(isLegacyAppPath('/chat/old-bookmark')).toBe(true)
    expect(isLegacyAppPath('/code/projects/example/extra')).toBe(false)
    expect(isLegacyAppPath('/%252fexample.com/code')).toBe(false)
  })

  it('keeps every real top-level path resolving', () => {
    // Product roots and resources parse as routes.
    for (const path of [...Object.values(PRODUCT_PATHS), '/workflows', '/bullpen/connect', '/goals', '/crew', '/code', '/brain', '/vault', '/relays', '/llm-gateway']) {
      expect(parseProductRoute(path), path).toBeTruthy()
    }
    // Old app pages keep the home fallback through the boundary.
    for (const root of ['settings', 'chat', 'users', 'providers', 'schedules']) {
      expect(LEGACY_APP_ROOTS.has(legacyAppRoot(`/${root}`)), root).toBe(true)
    }
    // Auth and share paths are handled before the product boundary; they
    // must never be mistaken for legacy pages either.
    for (const path of ['/login', '/auth/callback', '/oauth/consent', '/oauth/vault', '/oauth/cli', '/report', '/file', '/folder']) {
      expect(LEGACY_APP_ROOTS.has(legacyAppRoot(path)), path).toBe(false)
    }
    expect(LEGACY_APP_ROOTS.has(legacyAppRoot('/no-such-place'))).toBe(false)
  })

  it('refuses encoded redirect escapes, traversal, malformed resources and unknown routes', () => {
    for (const path of ['https://example.com/code', '//example.com/code', '/%2fexample.com/code', '/%252fexample.com/code', '/%255cexample.com', '/code/../login', '/code/%252e%252e/login', '/code/projects/%252fprivate', '/code/projects/example/extra', '/brain?folder=..%2Fprivate', '/brain?folder=%2Fprivate', '/brain?folder=C%3A%5Cprivate', '/brain?folder=Guides%2F%2Fprivate', '/constructor', '/__proto__', '/login', '/auth/callback', '/code/projects/%']) {
      expect(parseProductRoute(path), path).toBeUndefined()
      expect(productReturnPath(path), path).toBeNull()
    }
  })

  it('keeps the server OAuth return roots aligned with the client surface paths and aliases', () => {
    const source = readFileSync(new URL('../../../agent_go/cmd/server/product_url_auth.go', import.meta.url), 'utf8')
    const roots = JSON.parse('[' + source.match(/case (.*):/)![1] + ']') as string[]
    const clientRoots = ['', 'workflows', ...Object.values(PRODUCT_PATHS).map(path => path.slice(1)), ...Object.keys(PRODUCT_ROUTE_ALIASES)]
    expect(roots.sort()).toEqual([...new Set(clientRoots)].sort())
  })
})
