import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('ModePresetBar subscriptions', () => {
  it('selects derived session labels instead of raw session/tab records', () => {
    const bar = readFileSync('src/components/ModePresetBar.tsx', 'utf8')
    // Raw records change identity on every poll and chat event; subscribing
    // to them re-rendered the whole bar and made header clicks feel laggy.
    expect(bar).not.toContain('useChatStore(state => state.activeSessionsCache)')
    expect(bar).not.toContain('useChatStore(state => state.chatTabs)')
    expect(bar).toContain('currentSessionStatusLabel')
    expect(bar).toContain('currentTriggerLabel')
  })
})
