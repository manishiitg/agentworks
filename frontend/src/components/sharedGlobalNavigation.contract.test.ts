import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'

describe('shared AgentWorks and Crew global navigation shell', () => {
  it('mounts Ctrl+K above the product surface switch without an AgentWorks-only guard', () => {
    const source = readFileSync('src/App.tsx', 'utf8')
    const switcher = source.indexOf('{showQuickSwitcher && (')
    const surfaceSwitch = source.indexOf("{productSurface === 'video-studio' ? (")

    expect(switcher).toBeGreaterThan(0)
    expect(switcher).toBeLessThan(surfaceSwitch)
    expect(source).toContain("surface !== 'agentworks' && surface !== 'work'")
    expect(source.match(/surface !== 'code' && surface !== 'relays'/g)).toHaveLength(2)
    expect(source).toContain("productSurface !== 'code' && productSurface !== 'relays'")
  })

  it('keeps the global activity monitor visible in Crew reduced mode', () => {
    const source = readFileSync('src/components/ModePresetBar.tsx', 'utf8')
    expect(source).toContain('<GlobalActivityMonitor />')
    expect(source).not.toContain('!reduced && <GlobalActivityMonitor />')
  })
})
