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
  it('uses the platform phone, tablet and laptop defaults', () => {
    expect(parentWorkspaceWidth(1400, 'mobile', null)).toBe(494)
    expect(parentWorkspaceWidth(1400, 'tablet', null)).toBe(700)
    expect(parentWorkspaceWidth(1400, 'desktop', null)).toBe(920)
  })
  it('keeps both panes usable as the window and split change', () => {
    for (const available of [320, 480, 700, 1400, 3000]) {
      for (const ratio of [-1, 0, .2, .5, .8, 1, 2]) {
        const width = parentWorkspaceWidth(available, 'desktop', ratio)
        expect(width).toBeGreaterThanOrEqual(Math.min(240, available / 2))
        expect(available - width).toBeGreaterThanOrEqual(Math.min(240, available / 2))
      }
    }
  })
  it('remembers a separate draggable width for each device', () => {
    writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, .7, 'mobile')
    writeWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, .4, 'desktop')
    expect(readParentSplitRatio('mobile')).toBe(.7)
    expect(readParentSplitRatio('desktop')).toBe(.4)
    expect(readParentSplitRatio('tablet')).toBeNull()
    expect(parentWorkspaceWidth(1400, 'mobile', .7)).toBeCloseTo(420)
    expect(parentWorkspaceWidth(1400, 'mobile', .6)).toBe(560)
  })
})
