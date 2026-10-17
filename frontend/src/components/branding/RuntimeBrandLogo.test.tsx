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
  window.__APP_RUNTIME_CONFIG__ = { appName: 'customer', markUrl: '/brand/icon.svg', logoUrl: '/brand/logo.svg', logoDarkUrl: '/brand/logo-white.svg' }
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
  expect(image.alt).toBe('customer')
  expect(host.querySelectorAll('img')).toHaveLength(1)
  expect(host.querySelector('[data-runtime-brand="mark"]')?.getAttribute('title')).toBe('customer')
})
it('retains light and dark wordmarks in a horizontal header', async () => {
  await act(async () => root.render(<ProductTopBar sidebar={false}><RuntimeBrandLogo /></ProductTopBar>))
  expect(Array.from(host.querySelectorAll('img')).map(image => image.getAttribute('src'))).toEqual(['/brand/logo.svg', '/brand/logo-white.svg'])
})
it('fits a wordmark inside the rail when the deployment has no mark', async () => {
  window.__APP_RUNTIME_CONFIG__ = { appName: 'customer', logoUrl: '/brand/logo.svg', logoDarkUrl: '/brand/logo-white.svg' }
  await act(async () => root.render(<ProductTopBar sidebar><RuntimeBrandLogo /></ProductTopBar>))
  expect(host.querySelector('img')?.className).toContain('w-full object-contain')
  expect(host.querySelector('img')?.className).not.toContain('w-auto')
})

it('uses the default AgentWorks mark and name in the sidebar without custom branding', async () => {
  delete window.__APP_RUNTIME_CONFIG__
  await act(async () => root.render(<RuntimeBrandLogo variant="sidebar-header" />))
  expect(host.textContent).toBe('AgentWorks')
  expect(host.querySelector('svg')).not.toBeNull()
  expect(host.querySelector('img')).toBeNull()
})

it('pairs a custom square mark with the configured name in the sidebar', async () => {
  window.__APP_RUNTIME_CONFIG__ = { appName: 'Acme', markUrl: '/brand/acme.svg' }
  await act(async () => root.render(<RuntimeBrandLogo variant="sidebar-header" />))
  expect(host.textContent).toBe('Acme')
  expect(host.querySelector('img')?.getAttribute('src')).toBe('/brand/acme.svg')
  expect(host.querySelector('svg')).toBeNull()
})

it('uses the configured light and dark wordmarks for the sidebar header', async () => {
  await act(async () => root.render(<RuntimeBrandLogo variant="sidebar-header" />))
  expect(Array.from(host.querySelectorAll('img')).map(image => image.getAttribute('src'))).toEqual(['/brand/logo.svg', '/brand/logo-white.svg'])
  expect(host.querySelector('img')?.alt).toBe('customer')
  expect(host.querySelector('[data-runtime-brand="sidebar"]')?.getAttribute('title')).toBe('customer')
})

it('shows a custom name without introducing the default mark when no custom image exists', async () => {
  window.__APP_RUNTIME_CONFIG__ = { appName: 'Acme' }
  await act(async () => root.render(<RuntimeBrandLogo variant="sidebar-header" />))
  expect(host.textContent).toBe('Acme')
  expect(host.querySelector('svg, img')).toBeNull()
})
