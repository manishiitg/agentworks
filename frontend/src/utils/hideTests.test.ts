import { expect, it } from 'vitest'
import { isTestPath } from './hideTests'

it('recognises test files by their usual names only', () => {
  for (const path of ['auth/login.test.ts', 'cmd/server/login_test.go', 'tests/login.py', 'pkg/__tests__/a.js', 'app/test_login.py', 'src/LoginSpec.kt', 'web/login.spec.tsx'])
    expect(isTestPath(path), path).toBe(true)
  for (const path of ['auth/login.ts', 'docs/testing.md', 'src/latest.ts', 'contest/entry.go', 'README.md'])
    expect(isTestPath(path), path).toBe(false)
})
