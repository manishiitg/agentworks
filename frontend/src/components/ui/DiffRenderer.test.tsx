// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { DiffRenderer } from './DiffRenderer'
vi.mock('../../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'dark' }) }))
vi.mock('../../hooks/useMobileViewport', () => ({ useMobileViewport: () => false }))
vi.mock('./DiffCode', () => ({ default: () => null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-1002: opening a removed line must target the CURRENT after-line,
// while clicking its comment number still selects the original before-line.
it('keeps current editor coordinates separate from before-side comment anchors', async () => {
  const open = vi.fn(), comment = vi.fn()
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DiffRenderer content={'@@ -10,2 +20,2 @@\n-old\n+new\n context'} onOpenLine={open} onSelectLine={comment} />))
    const click = async (label: string) => { await act(async () => (host.querySelector(`[aria-label="${label}"]`) as HTMLButtonElement).click()) }
    await click('Comment on before line 10')
    expect(comment).toHaveBeenCalledWith({ side: 'before', line: 10 })
    expect(open).not.toHaveBeenCalled()
    await click('Open current file at line 20')
    expect(open).toHaveBeenCalledWith(20)
    expect(comment).toHaveBeenCalledTimes(1)
  } finally { await act(async () => root.unmount()) }
})
