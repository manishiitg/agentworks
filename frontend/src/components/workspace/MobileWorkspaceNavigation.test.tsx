// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ProductWorkspaceShell } from './ProductWorkspaceShell'
import { MobileWorkspaceNavigation } from './MobileWorkspaceNavigation'
vi.mock('./WorkspaceTopToolbar', () => ({ WorkspaceTopToolbar: ({ children, className }: { children: React.ReactNode; className?: string }) => <header className={className}>{children}<MobileWorkspaceNavigation /></header> }))

it('switches full-height phone panes without losing the mounted draft, and offers no Builder switch to shared users', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  function Workspace({ shared = false }: { shared?: boolean }) {
    const [pane, setPane] = useState<'chat' | 'workspace'>('chat')
    return <ProductWorkspaceShell chatOpen={!shared} panelOpen splitRatio={0.5} mobilePane={pane} onMobilePaneChange={setPane}
      showChatReopenControl={!shared} onOpenChat={() => {}} onOpenWorkspace={() => {}}
      tabs={null} toolbar={null} divider={null} chat={<textarea aria-label="Draft" defaultValue="Keep this draft" />} workspace={<p>Project settings</p>} />
  }
  try {
    await act(async () => root.render(<Workspace />))
    const draft = host.querySelector('textarea')!
    const workspace = host.querySelector<HTMLButtonElement>('[aria-label="Open workspace panel"]')!
    expect(host.querySelector('[aria-label="Workspace panes"]')).toBeNull()
    await act(async () => workspace.click())
    expect(host.querySelector('[aria-label="Close workspace panel"]')).not.toBeNull()
    expect(host.querySelector('textarea')).toBe(draft)
    expect(host.querySelector('header')?.className).not.toContain('max-md:hidden')
    expect(host.querySelector('main')?.className).toContain('hidden md:flex')
    expect(host.querySelector('aside')?.classList.contains('hidden')).toBe(false)
    expect(host.querySelector('[class*="grid-rows"]')?.className).not.toContain('row-start-3')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Close workspace panel"]')!.click())
    expect(draft.value).toBe('Keep this draft')
    expect(host.querySelector('aside')?.className).toContain('hidden md:block')
    await act(async () => root.render(<Workspace shared />))
    expect(host.querySelector('[aria-label="Workspace navigation"]')).toBeNull()
    expect(host.querySelector('main')).toBeNull()
    expect(host.querySelector('aside')?.classList.contains('hidden')).toBe(false)
  } finally { await act(async () => root.unmount()); host.remove() }
})
