// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

// Persisted stores need a Storage before they are created at import time.
vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})

vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcher from './QuickSwitcher'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { CHAT_SCROLL_TO_BOTTOM_EVENT } from '../utils/chatScrollRequest'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.useRealTimers() })

it('asks for the bottom once when switching chats, with no timed repeats', async () => {
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  useChatStore.setState({
    chatTabs: { t1: { tabId: 't1', name: 'Switch target chat', createdAt: 1, metadata: { mode: 'multi-agent' } } } as never,
    activeTabId: null,
  })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<QuickSwitcher isOpen onClose={vi.fn()} />) })
  await act(async () => { await Promise.resolve() })
  const row = [...host.querySelectorAll('.cursor-pointer')].find(div => div.textContent?.includes('Switch target chat'))
  expect(row).toBeTruthy()
  vi.useFakeTimers()
  const seen = vi.fn()
  window.addEventListener(CHAT_SCROLL_TO_BOTTOM_EVENT, seen)
  cleanups.push(() => window.removeEventListener(CHAT_SCROLL_TO_BOTTOM_EVENT, seen))
  await act(async () => { row!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
  await act(async () => { vi.advanceTimersByTime(2000) })
  expect(seen).toHaveBeenCalledTimes(1)
})
