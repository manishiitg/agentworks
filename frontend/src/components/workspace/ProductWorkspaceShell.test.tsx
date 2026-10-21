// @vitest-environment happy-dom
import { act, useEffect } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProductWorkspaceShell } from './ProductWorkspaceShell'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = '' })

describe('shared product workspace visibility', () => {
  it('shows the dashboard alone without mounting or reopening a hidden outer chat', async () => {
    const mounted = vi.fn()
    function Chat() { useEffect(() => { mounted() }, []); return <p>Outer conversation</p> }
    const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    await act(async () => root?.render(<ProductWorkspaceShell
      chatOpen={false} panelOpen splitRatio={.38} showChatReopenControl={false}
      onOpenChat={() => {}} onOpenWorkspace={() => {}}
      tabs={null} toolbar={null} chat={<Chat />} workspace={<p>Dashboard Ask</p>} divider="Resize"
    />))
    expect(host.textContent).toContain('Dashboard Ask')
    expect(host.querySelector('main')).toBeNull()
    expect(host.querySelector('[aria-label="Show chat panel"]')).toBeNull()
    expect(host.textContent).not.toContain('Resize')
    expect(mounted).not.toHaveBeenCalled()
  })

  it('keeps the reader mounted across phone pane switches and restores desktop panes', async () => {
    const mounted = vi.fn()
    function Reader() { useEffect(() => { mounted() }, []); return <p>Selected knowledge entry</p> }
    const host = document.createElement('div'); document.body.append(host); root = createRoot(host)
    const render = (mobilePane?: 'chat' | 'workspace') => act(async () => root?.render(<ProductWorkspaceShell
      chatOpen panelOpen splitRatio={.38} mobilePane={mobilePane}
      onOpenChat={() => {}} onOpenWorkspace={() => {}}
      tabs="Access chat" toolbar="Views" chat="Chat transcript" workspace={<Reader />} divider="Resize"
    />))
    await render()
    expect(host.querySelector('main')?.className).toMatch(/^flex /)
    expect(host.querySelector('aside')?.classList.contains('hidden')).toBe(false)
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
