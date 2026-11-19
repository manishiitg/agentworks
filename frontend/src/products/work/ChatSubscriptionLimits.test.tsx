// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { formatTokens } from '../../utils/formatTokens'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { renderToStaticMarkup } from 'react-dom/server'
vi.mock('./useChatContextView', () => ({ useChatContextView: () => ({session_id:'chat',used_tokens:288000,window_tokens:1000000,limits:[{label:'5h',used_percent:40},{label:'7d',used_percent:100,resets_at:'2026-11-20T12:00:00Z'}]}) }))
vi.mock('../../services/api', () => ({ agentApi: {} }))
vi.mock('../../hooks/useChatPreferences', () => ({ useChatPreferences: vi.fn() }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: {} }))
import ChatStatusBar from './ChatStatusBar'
import { ComposerDisplayMenu } from './ComposerDisplayMenu'
import { useChatDisplayStore } from '../../stores/useChatDisplayStore'
import ChatContextItems, { chatLimitsPercent, PlanWindowRow } from './ChatContextItems'

it('shows every reported subscription window, not just the fullest, without inventing absent windows', () => {
 const html=renderToStaticMarkup(<ChatStatusBar sessionId="chat" busy={false}/>)
 expect(html).toContain('5h 40% · 7d 100%')
 expect(chatLimitsPercent([{label:'5h',used_percent:0}])).toBe('5h 0%')
 expect(chatLimitsPercent(undefined)).toBe('')
 const detail=renderToStaticMarkup(<PlanWindowRow label="7d" percent={72} resets="Friday 12:00"/>)
 expect(detail).toContain('72% used · 28% left')
 expect(detail).toContain('Resets Friday 12:00')
})


it('keeps account limits separate from context and expands without closing the composer menu', async () => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  await act(async () => root.render(<Menu.Root open modal={false}><Menu.Trigger>Chat options</Menu.Trigger>
    <Menu.Content><ChatContextItems sessionId="chat" showDisplay={false} onOpenUsage={vi.fn()} /></Menu.Content>
  </Menu.Root>))
  const row = host.querySelector<HTMLElement>('[role="menuitem"][aria-expanded]')!
  expect(row.textContent).toContain('Usage & context')
  expect(row.textContent).toContain(formatTokens(288000)+' / '+formatTokens(1000000))
  expect(host.textContent).toContain('7-day limit reached')
  expect(host.textContent).not.toContain('This chat’s context')
  await act(async () => row.click())
  expect(row.getAttribute('aria-expanded')).toBe('true')
  expect(host.querySelector('[role="menu"]')).not.toBeNull()
  expect(host.textContent).toContain('Account limits')
  expect(host.textContent).toContain('This chat’s context')
  expect(host.textContent).toContain('Usage history')
  await act(async () => row.click())
  expect(host.textContent).not.toContain('Usage history')
 } finally { await act(async () => root.unmount()); host.remove() }
})

it('opens display and shortcut help together without changing a chat preference on hover', async () => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 const thinkingBefore = useChatDisplayStore.getState().showThinking
 try {
  await act(async () => root.render(<Menu.Root open modal={false}><Menu.Trigger>Chat options</Menu.Trigger>
    <Menu.Content><ComposerDisplayMenu busy={false} showContext={false} /></Menu.Content>
  </Menu.Root>))
  const row = host.querySelector<HTMLElement>('[role="menuitem"]')!
  expect(document.querySelector('[data-testid="composer-send-keys"]')).toBeNull()
  await act(async () => {
   row.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'mouse', bubbles: true }))
   await new Promise(resolve => setTimeout(resolve, 120))
  })
  expect(row.getAttribute('aria-expanded')).toBe('true')
  expect(document.querySelector('[data-testid="composer-send-keys"]')).not.toBeNull()
  const thinking = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitemcheckbox"]')).find(item => item.textContent === 'Thinking')!
  expect(thinking.getAttribute('aria-checked')).toBe(String(thinkingBefore))
  await act(async () => thinking.click())
  expect(thinking.getAttribute('aria-checked')).toBe(String(!thinkingBefore))
  expect(row.getAttribute('aria-expanded')).toBe('true')
 } finally {
  await act(async () => root.unmount()); host.remove()
  useChatDisplayStore.getState().setShowThinking(thinkingBefore)
 }
})
