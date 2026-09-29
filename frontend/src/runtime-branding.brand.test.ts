// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { applyRuntimeBranding, hexToHslTriplet, runtimeBrandAsset } from './runtime-branding'

describe('deployment branding', () => {
  it('turns a brand colour into the theme triplet and applies it over every theme', () => {
    expect(hexToHslTriplet('#109AAA')).toBe('186 83% 36%')
    expect(hexToHslTriplet('teal')).toBeNull()
    expect(hexToHslTriplet('#fff')).toBeNull()
    applyRuntimeBranding({ appName: 'Excellence Technologies', brandColor: '#109AAA' }, document)
    expect(document.title).toBe('Excellence Technologies')
    expect(document.documentElement.style.getPropertyValue('--primary')).toBe('186 83% 36%')
    expect(document.documentElement.style.getPropertyValue('--ring')).toBe('186 83% 36%')
  })

  it('accepts only same-origin asset paths for logos', () => {
    expect(runtimeBrandAsset('logoUrl', { logoUrl: '/brand/logo.svg' })).toBe('/brand/logo.svg')
    expect(runtimeBrandAsset('logoUrl', { logoUrl: 'https://evil.example/x.svg' })).toBeNull()
    expect(runtimeBrandAsset('markUrl', { markUrl: '//evil.example/x.svg' })).toBeNull()
    expect(runtimeBrandAsset('logoUrl', {})).toBeNull()
  })
})
