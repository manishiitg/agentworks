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
  window.sessionStorage.clear()
  Object.defineProperty(window, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => saved.get(key) ?? null,
    setItem: (key: string, value: string) => saved.set(key, value),
  } })
})

afterEach(async () => {
  await act(async () => { roots.splice(0).forEach(root => root.unmount()) })
  document.body.innerHTML = ''
  vi.useRealTimers()
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
  expect(host.querySelector('[data-product-navigation-mode]')?.className).toContain('w-0')
  expect(host.querySelector('[data-navigation-shortcut-hint]')?.textContent).toContain('Ctrl+K')
  expect(host.querySelector('[role="status"]')).toBeNull()

  const restored = await render()
  expect(restored.querySelector('[data-product-navigation-mode]')?.getAttribute('data-product-navigation-mode')).toBe('auto-hide')
  expect(restored.querySelector('[role="status"]')).toBeNull()
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

it('opens quick navigation from the hide hint and dismisses the hint', async () => {
  const host = await render()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!.click())
  const opened = vi.fn()
  window.addEventListener('open-quick-switcher', opened)
  try {
    const button = host.querySelector<HTMLButtonElement>('[aria-label="Open quick navigation (Ctrl+K or Command+K)"]')!
    await act(async () => button.click())
    expect(opened).toHaveBeenCalledTimes(1)
    expect(host.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
    expect(saved.get('product_navigation_mode')).toBe('auto-hide')
  } finally {
    window.removeEventListener('open-quick-switcher', opened)
  }
})

it('hides after ten minutes of navigation inactivity, and restarts after reopening', async () => {
  vi.useFakeTimers()
  const host = await render()
  const navigation = host.querySelector('nav')!
  const mode = () => host.querySelector('[data-product-navigation-mode]')?.getAttribute('data-product-navigation-mode')
  await act(async () => vi.advanceTimersByTime(9 * 60 * 1000))
  // Work outside the rail does not extend its deadline.
  await act(async () => document.body.dispatchEvent(new Event('pointermove', { bubbles: true })))
  await act(async () => vi.advanceTimersByTime(60 * 1000))
  expect(mode()).toBe('auto-hide')
  expect(navigation.isConnected).toBe(true)
  const opened = vi.fn()
  window.addEventListener('open-quick-switcher', opened)
  try {
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Open quick navigation (Ctrl+K or Command+K)"]')!.click())
    expect(opened).toHaveBeenCalledTimes(1)
  } finally { window.removeEventListener('open-quick-switcher', opened) }
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Show navigation"]')!.click())
  expect(mode()).toBe('fixed')
  await act(async () => vi.advanceTimersByTime(9 * 60 * 1000))
  await act(async () => navigation.dispatchEvent(new Event('pointermove', { bubbles: true })))
  await act(async () => vi.advanceTimersByTime(9 * 60 * 1000))
  expect(mode()).toBe('fixed')
  // Switching products must preserve the same deadline.
  const restored = await render()
  await act(async () => vi.advanceTimersByTime(60 * 1000))
  expect(mode()).toBe('auto-hide')
  expect(restored.querySelector('[data-product-navigation-mode]')?.getAttribute('data-product-navigation-mode')).toBe('auto-hide')
})

it('dismisses the hint when quick navigation opens and shows it again on the next manual hide', async () => {
  vi.useFakeTimers()
  const host = await render()
  const pin = host.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!
  await act(async () => pin.click())
  expect(host.querySelector('[data-navigation-shortcut-hint]')).not.toBeNull()
  // App emits this for Ctrl+K, Command+K and all other switcher entry points.
  await act(async () => window.dispatchEvent(new CustomEvent('quick-switcher-opened')))
  expect(host.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
  expect(host.querySelector('[aria-label="Show navigation"]')).not.toBeNull()
  const restored = await render()
  expect(restored.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
  // Focus/visibility checks on an already-hidden rail must not revive the hint.
  await act(async () => vi.advanceTimersByTime(11 * 60 * 1000))
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(host.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
  await act(async () => pin.click())
  await act(async () => pin.click())
  expect(host.querySelector('[data-navigation-shortcut-hint]')).not.toBeNull()
  expect(host.querySelector('[role="status"]')).toBeNull()
})


it('expires the shortcut hint after eight seconds across remounts and keeps the edge reopen control', async () => {
  vi.useFakeTimers()
  const host = await render()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!.click())
  expect(host.querySelector('[data-navigation-shortcut-hint]')).not.toBeNull()
  await act(async () => vi.advanceTimersByTime(4_000))
  const previousRoot = roots.shift()!
  await act(async () => previousRoot.unmount())
  const restored = await render()
  await act(async () => vi.advanceTimersByTime(3_999))
  expect(restored.querySelector('[data-navigation-shortcut-hint]')).not.toBeNull()
  await act(async () => vi.advanceTimersByTime(1))
  expect(restored.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
  expect(restored.querySelector('[aria-label="Show navigation"]')).not.toBeNull()
  const reloaded = await render()
  expect(reloaded.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(restored.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
  await act(async () => restored.querySelector<HTMLButtonElement>('[aria-label="Show navigation"]')!.click())
  await act(async () => restored.querySelector<HTMLButtonElement>('[aria-label="Keep navigation fixed"]')!.click())
  expect(restored.querySelector('[data-navigation-shortcut-hint]')).not.toBeNull()
  await act(async () => vi.advanceTimersByTime(8_000))
  expect(restored.querySelector('[data-navigation-shortcut-hint]')).toBeNull()
})
