// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProductTopBar } from '../workspace/ProductTopBar'
import { RuntimeBrandLogo } from './RuntimeBrandLogo'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: Root
beforeEach(() => {
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {} })
  window.__APP_RUNTIME_CONFIG__ = { appName: 'Confida', markUrl: '/brand/icon.svg', logoUrl: '/brand/logo.svg', logoDarkUrl: '/brand/logo-white.svg' }
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount()); host.remove()
  delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllGlobals()
})
it('uses the square deployment mark in the narrow rail', async () => {
  await act(async () => root.render(<ProductTopBar sidebar><RuntimeBrandLogo /></ProductTopBar>))
  const image = host.querySelector('img')!
  expect(image.getAttribute('src')).toBe('/brand/icon.svg')
  expect(image.alt).toBe('Confida')
  expect(host.querySelectorAll('img')).toHaveLength(1)
  expect(host.querySelector('[data-runtime-brand="mark"]')?.getAttribute('title')).toBe('Confida')
})
it('retains light and dark wordmarks in a horizontal header', async () => {
  await act(async () => root.render(<ProductTopBar sidebar={false}><RuntimeBrandLogo /></ProductTopBar>))
  expect(Array.from(host.querySelectorAll('img')).map(image => image.getAttribute('src'))).toEqual(['/brand/logo.svg', '/brand/logo-white.svg'])
})
it('fits a wordmark inside the rail when the deployment has no mark', async () => {
  window.__APP_RUNTIME_CONFIG__ = { appName: 'Confida', logoUrl: '/brand/logo.svg', logoDarkUrl: '/brand/logo-white.svg' }
  await act(async () => root.render(<ProductTopBar sidebar><RuntimeBrandLogo /></ProductTopBar>))
  expect(host.querySelector('img')?.className).toContain('w-full object-contain')
  expect(host.querySelector('img')?.className).not.toContain('w-auto')
})
