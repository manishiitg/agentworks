// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import FileContextDisplay from './FileContextDisplay'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

vi.mock('./ui/WorkspaceImage', () => ({ WorkspaceImage: ({ alt }: { alt: string }) => <img alt={alt} /> }))

it('previews image attachments beside file chips without changing removal paths or submitting the draft', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const remove = vi.fn()
  const path = 'Crew/example/uploads/pasted-image.png'
  try {
    await act(async () => root.render(<FileContextDisplay attachmentMode files={[{ path, name: 'Screenshot', type: 'file' }, { path: 'Crew/example/report.pdf', name: 'Report.pdf', type: 'file' }]} onRemoveFile={remove} onClearAll={vi.fn()} agentMode="multi-agent" isRequiredFolderSelected />))
    expect(host.querySelector('img')?.alt).toBe('Screenshot')
    expect(host.textContent).toContain('Report.pdf')
    const button = host.querySelector<HTMLButtonElement>('[aria-label="Remove Screenshot"]')!
    expect(button.type).toBe('button')
    await act(async () => button.click())
    expect(remove).toHaveBeenCalledWith(path)
  } finally { await act(async () => root.unmount()) }
})

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
