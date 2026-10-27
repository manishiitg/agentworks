import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { writeWorkflowSplitPreference } from '../../../utils/reportPreviewPreference'
import { parentWorkspaceWidth, readParentSplitRatio, SPARKQUILL_PREVIEW_SCOPE } from './parentSplit'

beforeEach(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('window', { localStorage: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  } })
})
afterEach(() => vi.unstubAllGlobals())

describe('SparkQuill parent split', () => {
  it('keeps both panes usable as the window and split change', () => {
    for (const available of [320, 480, 700, 1400, 3000]) {
      for (const ratio of [-1, 0, .2, .5, .8, 1, 2]) {
        const width = parentWorkspaceWidth(available, ratio)
        expect(width).toBeGreaterThanOrEqual(Math.min(240, available / 2))
        expect(available - width).toBeGreaterThanOrEqual(Math.min(240, available / 2))
      }
    }
  })
  it('remembers one draggable width without a device mode', () => {
    expect(readParentSplitRatio()).toBeNull()
    expect(parentWorkspaceWidth(1400, null)).toBe(700)
    writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, .7)
    expect(readParentSplitRatio()).toBe(.7)
    expect(parentWorkspaceWidth(1400, .7)).toBeCloseTo(420)
  })
})
