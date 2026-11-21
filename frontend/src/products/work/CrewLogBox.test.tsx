// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewLogBox } from './CrewLogBox'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function mount(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  const text = Array.from({ length: 200 }, (_, index) => `line ${index + 1}`).join('\n')
  await act(async () => root.render(<CrewLogBox title="Output" text={text} />))
  return { host, text, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows the log in a scrollable box', async () => {
  const view = await mount(vi.fn().mockResolvedValue(undefined))
  try {
    const pre = view.host.querySelector('pre')!
    expect(pre.className).toContain('overflow-auto')
    expect(pre.className).toContain('max-h-')
    expect(pre.textContent).toBe(view.text)
  } finally { await view.cleanup() }
})

it('copies the whole log, not only what is on screen, and says so', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  const view = await mount(writeText)
  try {
    await act(async () => { (view.host.querySelector('button[aria-label="Copy the whole log"]') as HTMLButtonElement).click() })
    expect(writeText).toHaveBeenCalledWith(view.text)
    expect(view.host.textContent).toContain('Copied')
  } finally { await view.cleanup() }
})

it('stays quiet when the browser refuses the copy', async () => {
  const view = await mount(vi.fn().mockRejectedValue(new Error('denied')))
  try {
    await act(async () => { (view.host.querySelector('button[aria-label="Copy the whole log"]') as HTMLButtonElement).click() })
    expect(view.host.textContent).not.toContain('Copied')
  } finally { await view.cleanup() }
})
