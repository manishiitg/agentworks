import { describe, expect, it } from 'vitest'
import { formatDateTime, mergeServerRows, normalizeServerKey, parseMcpServersJson, parseToolArgs, plural, slugifyId } from './gatewayConsoleUtils'
import type { GatewayConnector, GatewayProvider } from './gatewayAdminApi'

function connector(id: string, provider: string): GatewayConnector {
  return { ID: id, WorkspaceID: 'w1', Provider: provider, InstanceSlug: '', Label: provider, UpstreamURL: 'https://x/mcp', Status: 'active' }
}

function provider(name: string, key: string): GatewayProvider {
  return { Name: name, Key: key, URL: 'https://x/mcp', OAuth: false }
}

describe('plural', () => {
  it('uses the singular form for exactly one', () => {
    expect(plural(1, 'server')).toBe('1 server')
    expect(plural(0, 'server')).toBe('0 servers')
    expect(plural(2, 'server')).toBe('2 servers')
  })
})

describe('parseMcpServersJson', () => {
  it('reads the Claude-style mcpServers map', () => {
    const out = parseMcpServersJson('{"mcpServers": {"acme": {"url": "https://acme.example.com/mcp"}}}')
    expect(out.servers).toEqual([{ name: 'acme', url: 'https://acme.example.com/mcp' }])
    expect(out.skipped).toEqual([])
  })

  it('accepts a bare name-to-entry map and skips non-URL servers', () => {
    const out = parseMcpServersJson('{"acme": {"url": "https://acme.example.com/mcp"}, "local": {"command": "npx"}}')
    expect(out.servers).toEqual([{ name: 'acme', url: 'https://acme.example.com/mcp' }])
    expect(out.skipped).toEqual([{ name: 'local', reason: 'only Streamable-HTTP ("url") servers are supported' }])
  })

  it('rejects invalid JSON and empty maps', () => {
    expect(() => parseMcpServersJson('{nope')).toThrow('not valid JSON')
    expect(() => parseMcpServersJson('{"mcpServers": {}}')).toThrow('No servers found')
    expect(() => parseMcpServersJson('[]')).toThrow('Expected { "mcpServers"')
  })
})

describe('parseToolArgs', () => {
  it('returns null when the upstream sent no schema', () => {
    expect(parseToolArgs(null)).toBeNull()
    expect(parseToolArgs('!!!not-base64!!!')).toBeNull()
  })

  it('flattens properties with required flags', () => {
    const b64 = btoa(JSON.stringify({
      properties: {
        q: { type: 'string', description: 'Query' },
        limit: { type: ['number', 'null'] },
      },
      required: ['q'],
    }))
    expect(parseToolArgs(b64)).toEqual([
      { name: 'q', type: 'string', required: true, description: 'Query' },
      { name: 'limit', type: 'number | null', required: false, description: '' },
    ])
  })
})

describe('slugifyId', () => {
  it('derives a lowercase dashed id from a display name', () => {
    expect(slugifyId('Support engineers')).toBe('support-engineers')
    expect(slugifyId('  Data_Science 2!')).toBe('data-science-2')
    expect(slugifyId('!!!')).toBe('')
  })
})

describe('formatDateTime', () => {
  it('renders never for empty values', () => {
    expect(formatDateTime('')).toBe('never')
  })
})

describe('normalizeServerKey', () => {
  it('lowercases and strips non-alphanumerics like the gateway catalog', () => {
    expect(normalizeServerKey('MongoDB')).toBe('mongodb')
    expect(normalizeServerKey('AWS Knowledge')).toBe('awsknowledge')
    expect(normalizeServerKey('acme-notes_2')).toBe('acmenotes2')
  })
})

describe('mergeServerRows', () => {
  it('merges both sides by normalized name and attaches the catalog match', () => {
    const rows = mergeServerRows(
      [
        { name: 'Notion', connection: 'connected', status: 'ready', toolCount: 5 },
        { name: 'Sentry', connection: 'available', toolCount: 0 },
      ],
      [connector('c1', 'notion')],
      [provider('Notion', 'notion'), provider('Linear', 'linear')],
    )

    expect(rows.map((r) => r.name)).toEqual(['Notion', 'Sentry'])
    const notion = rows[0]
    expect(notion.agentworks?.connection).toBe('connected')
    expect(notion.gateway.map((c) => c.ID)).toEqual(['c1'])
    expect(notion.catalogMatch?.Name).toBe('Notion')
    expect(rows[1].gateway).toEqual([])
    expect(rows[1].catalogMatch).toBeNull()
  })

  it('keeps gateway-only connectors and groups multi-instance providers', () => {
    const rows = mergeServerRows([], [connector('c1', 'linear'), connector('c2', 'linear')], [provider('Linear', 'linear')])

    expect(rows).toHaveLength(1)
    expect(rows[0].name).toBe('linear')
    expect(rows[0].agentworks).toBeNull()
    expect(rows[0].gateway.map((c) => c.ID)).toEqual(['c1', 'c2'])
    expect(rows[0].catalogMatch?.Name).toBe('Linear')
  })
})
