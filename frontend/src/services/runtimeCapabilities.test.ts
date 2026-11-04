import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { isBrowserCDPEnabled } from '../utils/runtimeCapabilities'

type RuntimeWindow = Window & {
  __APP_RUNTIME_CONFIG__?: { cdpEnabled?: boolean | string }
}

describe('isBrowserCDPEnabled', () => {
  beforeEach(() => {
    vi.stubGlobal('window', {})
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('honors a disabled server runtime capability', () => {
    ;(window as RuntimeWindow).__APP_RUNTIME_CONFIG__ = { cdpEnabled: false }
    expect(isBrowserCDPEnabled()).toBe(false)
  })
})
