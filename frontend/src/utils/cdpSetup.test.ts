import { describe, expect, it } from 'vitest'

import {
  chromeCdpInstallCommand,
  chromeCdpLaunchCommand,
  chromeCdpVerifyCommand,
} from './cdpSetup'

describe('CDP setup commands', () => {
  it('keeps port 9222 as the installer default', () => {
    expect(chromeCdpInstallCommand()).toMatch(/\| bash$/)
    expect(chromeCdpInstallCommand(9222)).toMatch(/\| bash$/)
  })

  it('passes an additional port to the installer', () => {
    expect(chromeCdpInstallCommand(9333)).toMatch(/\| bash -s -- --port 9333$/)
  })

  it('uses a distinct profile and verification endpoint for another port', () => {
    expect(chromeCdpLaunchCommand(9333, 'MacIntel')).toContain('.chrome-cdp-profile-9333')
    expect(chromeCdpLaunchCommand(9333, 'MacIntel')).toContain('--remote-debugging-port=9333')
    expect(chromeCdpVerifyCommand(9333)).toBe('curl http://127.0.0.1:9333/json/version')
  })

})
