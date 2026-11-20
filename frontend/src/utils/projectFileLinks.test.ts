import { expect, it } from 'vitest'
import { parseCodeRef } from './projectFileLinks'

// Real reply text: agents cite code as `path/file.ext:line`; other inline code must stay plain.
it('reads code paths with an optional line and leaves other inline code alone', () => {
  expect(parseCodeRef('agent_go/cmd/server/server.go:5896')).toEqual({ path: 'agent_go/cmd/server/server.go', line: 5896 })
  expect(parseCodeRef('frontend/src/App.tsx')).toEqual({ path: 'frontend/src/App.tsx' })
  expect(parseCodeRef('./src/main.ts:12:4')).toEqual({ path: 'src/main.ts', line: 12 })
  expect(parseCodeRef('server.go:42')).toEqual({ path: 'server.go', line: 42 })
  for (const plain of ['server.go', 'GH_TOKEN', 'git push', 'https://example.com/a.js', '/etc/hosts.conf', ':50', 'a/b', 'npm run build']) expect(parseCodeRef(plain)).toBeNull()
})
