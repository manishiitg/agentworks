// @vitest-environment happy-dom
import { act, useEffect, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProductWorkspaceShell } from './ProductWorkspaceShell'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = '' })

describe('shared product workspace visibility', () => {
  it('shows the shared dashboard without an empty tab header or hidden outer chat (PLAT-746)', async () => {
    const mounted = vi.fn()
    function Chat() { useEffect(() => { mounted() }, []); return <p>Outer conversation</p> }
    const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    await act(async () => root?.render(<ProductWorkspaceShell
      chatOpen={false} panelOpen splitRatio={.38} showChatReopenControl={false} showToolbar={false}
      onOpenChat={() => {}} onOpenWorkspace={() => {}}
      tabs={null} toolbar={null} chat={<Chat />} workspace={<p>Dashboard Ask</p>} divider="Resize"
    />))
    expect(host.textContent).toContain('Dashboard Ask')
    expect(host.querySelector('[data-terminal-focus-chrome="toolbar"]')).toBeNull()
    expect(host.querySelector('main')).toBeNull()
    expect(host.querySelector('[aria-label="Show chat panel"]')).toBeNull()
    expect(host.textContent).not.toContain('Resize')
    expect(mounted).not.toHaveBeenCalled()
  })

  it('reopens the selected panel from the same header control and retains content across desktop and phone switches', async () => {
    const mounted = vi.fn()
    function Reader() { useEffect(() => { mounted() }, []); return <p>Selected knowledge entry</p> }
    const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    function Fixture({ mobilePane }: { mobilePane?: 'chat' | 'workspace' }) {
      const [open, setOpen] = useState(true)
      return <ProductWorkspaceShell
      chatOpen panelOpen={open} retainWorkspace toolbarWhenPanelClosed compactContext splitRatio={.38} mobilePane={mobilePane}
      onOpenChat={() => {}} onOpenWorkspace={() => setOpen(true)} onCloseWorkspace={() => setOpen(false)}
      tabs="Access chat" toolbar="Views" chat="Chat transcript" workspace={<Reader />} divider="Resize"
    />
    }
    const render = (mobilePane?: 'chat' | 'workspace') => act(async () => root?.render(<Fixture mobilePane={mobilePane} />))
    await render()
    expect(host.querySelector('[data-terminal-focus-chrome="toolbar"]')?.textContent).toContain('Access chat')
    expect(host.querySelectorAll('[aria-label="Close workspace panel"]')).toHaveLength(1)
    expect(host.querySelector('[data-workspace-panel-edge]')).toBeNull()
    expect(host.querySelector('main')?.className).toMatch(/^flex /)
    expect(host.querySelector('aside')?.classList.contains('hidden')).toBe(false)
    const close = host.querySelector<HTMLButtonElement>('[aria-label="Close workspace panel"]')!
    expect(close.closest('aside')).toBeNull()
    expect(close.closest('[data-terminal-focus-chrome="toolbar"]')).not.toBeNull()
    expect(close.querySelector('.lucide-panel-right-close')).not.toBeNull()
    await act(async () => close.click())
    expect(host.querySelector('aside')?.hidden).toBe(true)
    const open = host.querySelector<HTMLButtonElement>('[aria-label="Open workspace panel"]')!
    expect(open.closest('[data-terminal-focus-chrome="toolbar"]')).not.toBeNull()
    expect(open.getAttribute('aria-expanded')).toBe('false')
    expect(open.querySelector('.lucide-panel-right-open')).not.toBeNull()
    await act(async () => open.click())
    expect(host.querySelector('aside')?.hidden).toBe(false)
    await render('workspace')
    expect(host.querySelector('main')?.className).toMatch(/^hidden md:flex /)
    expect(host.querySelector('aside')?.classList.contains('hidden')).toBe(false)
    await render('chat')
    expect(host.querySelector('main')?.className).toMatch(/^flex /)
    expect(host.querySelector('aside')?.className).toMatch(/^hidden md:block /)
    expect(host.textContent).toContain('Selected knowledge entry')
    await render('workspace')
    expect(mounted).toHaveBeenCalledOnce()
  })
})

it('expands management when its secondary assistant closes and retains the existing draft', async () => {
  const mounted = vi.fn()
  function Assistant() { useEffect(() => { mounted() }, []); return <textarea aria-label="Assistant draft" /> }
  const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  const render = (open: boolean, retain = true) => act(async () => root?.render(<ProductWorkspaceShell chatOpen panelOpen={open} retainWorkspace={retain} toolbarWhenPanelClosed splitRatio={.6}
    onOpenChat={() => {}} onOpenWorkspace={() => {}} tabs="Vault" toolbar={null} chat="Management" workspace={<Assistant />} divider="Resize" />))
  await render(false, false)
  expect(host.querySelector('aside')).toBeNull()
  expect(mounted).not.toHaveBeenCalled()
  await render(true)
  const draft = host.querySelector<HTMLTextAreaElement>('textarea')!
  draft.value = 'Keep this unsent'
  await render(false)
  expect(host.querySelector('aside')?.hidden).toBe(true)
  expect(host.querySelector('[aria-label="Show workspace"]')).toBeNull()
  expect(host.textContent).not.toContain('Resize')
  await render(true)
  expect(host.querySelector('textarea')).toBe(draft)
  expect(draft.value).toBe('Keep this unsent')
  expect(mounted).toHaveBeenCalledOnce()
})
