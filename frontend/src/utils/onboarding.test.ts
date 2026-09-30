// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { dismissWorkflowWalkthrough, isWorkflowWalkthroughDismissed } from './onboarding'

const originalStorage = Object.getOwnPropertyDescriptor(window, 'localStorage')
beforeEach(() => {
  const values = new Map<string, string>()
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    get: () => ({
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value) },
      clear: () => values.clear(),
    }),
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  if (originalStorage) Object.defineProperty(window, 'localStorage', originalStorage)
  else Reflect.deleteProperty(window, 'localStorage')
  delete window.electronAPI
})

describe('desktop walkthrough persistence', () => {
  it('keeps a dismissed tour quiet when a restart has an empty origin store', () => {
    const saved = new Set<string>()
    window.electronAPI = {
      isWalkthroughDismissed: key => saved.has(key),
      dismissWalkthrough: key => { saved.add(key) },
    }
    dismissWorkflowWalkthrough('code')
    window.localStorage.clear() // New localhost port on the next launch.
    expect(isWorkflowWalkthroughDismissed('code')).toBe(true)
    expect(isWorkflowWalkthroughDismissed('crew')).toBe(false)
  })

  it('migrates existing origin dismissals into the desktop profile', () => {
    const dismiss = vi.fn()
    window.electronAPI = { isWalkthroughDismissed: () => false, dismissWalkthrough: dismiss }
    const key = 'agentworks_automation_walkthrough_v3_dismissed'
    window.localStorage.setItem(key, 'true')
    expect(isWorkflowWalkthroughDismissed('automation')).toBe(true)
    expect(dismiss).toHaveBeenCalledWith(key)
  })

  it('uses desktop persistence even when localStorage is unavailable', () => {
    const saved = new Set<string>()
    window.electronAPI = {
      isWalkthroughDismissed: key => saved.has(key),
      dismissWalkthrough: key => { saved.add(key) },
    }
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => { throw new Error('Unavailable') })
    dismissWorkflowWalkthrough('overview')
    expect(isWorkflowWalkthroughDismissed('overview')).toBe(true)
  })

  it('retains browser-only dismissal without an Electron bridge', () => {
    dismissWorkflowWalkthrough('crew')
    expect(isWorkflowWalkthroughDismissed('crew')).toBe(true)
    expect(isWorkflowWalkthroughDismissed('code')).toBe(false)
  })
})
