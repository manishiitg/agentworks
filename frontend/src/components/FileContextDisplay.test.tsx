// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import FileContextDisplay from './FileContextDisplay'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows next-message attachment names while remove still targets the exact stored path', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const remove = vi.fn()
  const clear = vi.fn()
  const path = 'Crew/private-project-123/uploads/pasted-image.png'
  try {
    await act(async () => root.render(<FileContextDisplay files={[{ path, name: 'Pasted image', type: 'file' }]} onRemoveFile={remove} onClearAll={clear} agentMode="multi-agent" isRequiredFolderSelected />))
    expect(host.textContent).toContain('For next message:')
    expect(host.textContent).toContain('Pasted image')
    expect(host.innerHTML).not.toContain('private-project-123')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Remove Pasted image"]')!.click())
    expect(remove).toHaveBeenCalledWith(path)
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Clear')!.click())
    expect(clear).toHaveBeenCalledOnce()
  } finally { await act(async () => root.unmount()) }
})
