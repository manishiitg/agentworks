import { expect, it } from 'vitest'
import { shellStreamUrl } from './codeShellPanelHelpers'

// #2831: new URL('/api/...', savedBase) silently connected to the local
// backend instead of the selected server, which reported a disconnected shell.
it('keeps the selected desktop connection in the project Terminal WebSocket URL', () => {
  for (const [base, path] of [
    ['http://127.0.0.1:18743/api/connections/saved', '/api/connections/saved'],
    ['https://example.com/', ''],
  ]) {
    const url = new URL(shellStreamUrl('project', 80, 24, base, 'local-test-token', 2, 'work', { kind: 'mac_app', version: '1.2.3' }))
    expect(url.pathname).toBe(`${path}/api/agent-profiles/work/projects/project/shell/stream`)
    expect(url.protocol).toBe(base.startsWith('https') ? 'wss:' : 'ws:')
    expect(url.searchParams.get('token')).toBe('local-test-token')
    expect(url.searchParams.get('tab')).toBe('2')
    expect(url.searchParams.get('client_version')).toBe('1.2.3')
  }
})
