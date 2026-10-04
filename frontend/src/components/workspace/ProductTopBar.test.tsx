// @vitest-environment happy-dom
import React, { act, useEffect } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProductTopBar, ProductTopBarActions } from './ProductTopBar'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const saved = new Map<string, string>()
const roots: Root[] = []

beforeEach(() => {
  saved.clear()
  Object.defineProperty(window, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => saved.get(key) ?? null,
    setItem: (key: string, value: string) => saved.set(key, value),
  } })
})

afterEach(async () => {
  await act(async () => { roots.splice(0).forEach(root => root.unmount()) })
  document.body.innerHTML = ''
})

async function render(children: React.ReactNode = <ProductTopBarActions>Account</ProductTopBarActions>) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  roots.push(root)
  await act(async () => root.render(<ProductTopBar>{children}</ProductTopBar>))
  return host
}

it('defaults to fixed and restores the chosen mode after remounting', async () => {
  const host = await render()
  const pin = host.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!
  expect(pin.getAttribute('aria-pressed')).toBe('true')
  expect(host.querySelector('[aria-label="Show navigation"]')).toBeNull()
  await act(async () => pin.click())
  expect(saved.get('product_navigation_mode')).toBe('auto-hide')
  expect(pin.getAttribute('aria-pressed')).toBe('false')
  expect(host.querySelector('[aria-label="Show navigation"]')).not.toBeNull()

  const restored = await render()
  expect(restored.querySelector('[data-product-navigation-mode]')?.getAttribute('data-product-navigation-mode')).toBe('auto-hide')
  await act(async () => restored.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!.click())
  expect(saved.get('product_navigation_mode')).toBe('fixed')
})

it('keeps live navigation children mounted when hiding and pinning the strip', async () => {
  const mounted = vi.fn()
  const stopped = vi.fn()
  function LiveMonitor() {
    useEffect(() => { mounted(); return stopped }, [])
    return <button>Active work</button>
  }
  const host = await render(<LiveMonitor />)
  const pin = host.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!
  await act(async () => pin.click())
  await act(async () => pin.click())
  expect(mounted).toHaveBeenCalledTimes(1)
  expect(stopped).not.toHaveBeenCalled()
  expect(host.textContent).toContain('Active work')
})

it('uses fixed navigation for an unknown saved preference', async () => {
  saved.set('product_navigation_mode', 'retired')
  const host = await render()
  expect(host.querySelector('[data-product-navigation-mode]')?.getAttribute('data-product-navigation-mode')).toBe('fixed')
})
