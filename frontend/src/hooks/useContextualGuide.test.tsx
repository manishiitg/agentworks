// @vitest-environment happy-dom
import React, { act, useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useContextualGuide } from './useContextualGuide'
import { contextualGuideKey } from '../utils/onboarding'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const storageDescriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
let values: Map<string, string>
beforeEach(() => {
  vi.useFakeTimers(); values = new Map()
  Object.defineProperty(window, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
  } })
})
afterEach(() => {
  vi.useRealTimers()
  if (storageDescriptor) Object.defineProperty(window, 'localStorage', storageDescriptor)
  delete window.electronAPI; document.body.innerHTML = ''
})
function Tip({ topic, visible = true }: { topic: string; visible?: boolean }) {
  const anchor = useRef<HTMLDivElement>(null)
  const tip = useContextualGuide(contextualGuideKey('code', topic), anchor)
  return <div ref={element => {
    anchor.current = element
    if (element) element.getBoundingClientRect = () => ({ width: visible ? 30 : 0, height: 30, top: 20, bottom: 50, left: 20, right: 50 }) as DOMRect
  }}><button>Help</button>{tip.open && <div data-contextual-guide>Tip for {topic}<button onClick={tip.dismiss}>Got it</button></div>}</div>
}
it('shows matching first-visit tips and remembers them across remounts and renderer origins', async () => {
  const saved = new Set<string>()
  window.electronAPI = { isWalkthroughDismissed: key => saved.has(key), dismissWalkthrough: key => { saved.add(key) } }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<Tip topic="Test Files" />))
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).toContain('Tip for Test Files')
    expect(saved.has(contextualGuideKey('code', 'Test Files'))).toBe(true)
    await act(async () => root.render(<Tip topic="Test Costs" />))
    expect(host.textContent).not.toContain('Tip for Test Files')
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).toContain('Tip for Test Costs')
    values.clear()
    await act(async () => root.render(<Tip key="remount" topic="Test Files" />))
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).not.toContain('Tip for')
  } finally { await act(async () => root.unmount()) }
})
it('waits for a visible section and yields to the main walkthrough', async () => {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const tour = document.createElement('div'); tour.dataset.testid = 'workflow-walkthrough-dialog'; document.body.append(tour)
  try {
    await act(async () => root.render(<Tip topic="Blocked Test" visible={false} />))
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).not.toContain('Tip for')
    await act(async () => root.render(<Tip topic="Blocked Test" visible />))
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).not.toContain('Tip for')
    await act(async () => { tour.remove() })
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).toContain('Tip for Blocked Test')
    await act(async () => document.body.append(tour))
    expect(host.textContent).not.toContain('Tip for')
  } finally { await act(async () => root.unmount()) }
})
it('waits until the user stops typing', async () => {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const input = document.createElement('textarea'); document.body.append(input); input.focus()
  try {
    await act(async () => root.render(<Tip topic="Editing Test" />))
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).not.toContain('Tip for')
    await act(async () => input.blur())
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.textContent).toContain('Tip for Editing Test')
  } finally { await act(async () => root.unmount()) }
})
it('shows only one automatic tip when multiple sections mount together', async () => {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<><Tip topic="Concurrent A" /><Tip topic="Concurrent B" /></>))
    await act(async () => vi.advanceTimersByTime(600))
    expect(host.querySelectorAll('[data-contextual-guide]')).toHaveLength(1)
  } finally { await act(async () => root.unmount()) }
})
