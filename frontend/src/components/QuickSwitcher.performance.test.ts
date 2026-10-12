import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const read = (path: string) => readFileSync(path, 'utf8')

describe('QuickSwitcher open-path performance contract', () => {
  it('keeps the Ctrl+K handler eager and warms the lazy dialog chunk', () => {
    const app = read('src/App.tsx')
    const switcher = read('src/components/QuickSwitcher.tsx')

    expect(app).toContain("from './components/QuickSwitcherHost'")
    expect(app).not.toContain("import QuickSwitcher from './components/QuickSwitcher'")
    expect(app).toContain('preloadQuickSwitcher')
    expect(switcher).toContain('getActiveSessions()')
    expect(switcher).not.toContain('getActiveSessions(true)')
    expect(switcher).toContain('}, [isOpen, initialQuery])')
  })
})
