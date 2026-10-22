// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { DashboardLayoutActions, DashboardLayoutControls, DashboardLayoutProvider } from './DashboardLayoutControls'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('keeps sizing inside the dashboard header with keyboard close and the original layout callbacks', async () => {
  const width = vi.fn(), device = vi.fn(), toggle = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<DashboardLayoutProvider value={<DashboardLayoutControls width={50} onWidthChange={width} previewDevice="tablet" onPreviewDeviceChange={device} chatVisible onToggleChat={toggle} />}><header><DashboardLayoutActions /></header><main /></DashboardLayoutProvider>))
    const button = host.querySelector<HTMLButtonElement>('[aria-label="Dashboard layout"]')!
    expect(button.closest('header')).not.toBeNull()
    expect(host.querySelector('[role="separator"]')).toBeNull()
    await act(async () => button.click())
    expect(host.textContent).not.toContain('Close dashboard panel')
    const range = host.querySelector<HTMLInputElement>('input[type="range"]')!
    expect(range.getAttribute('aria-label')).toBe('Dashboard width')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Mobile preview"]')!.click())
    expect(device).toHaveBeenCalledExactlyOnceWith('mobile')
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(host.querySelector('input')).toBeNull()
    expect(document.activeElement).toBe(button)
    await act(async () => button.click())
    await act(async () => [...host.querySelectorAll('button')].find(item => item.textContent === 'Expand dashboard')!.click())
    expect(toggle).toHaveBeenCalledOnce()
    expect(host.querySelector('input')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})
