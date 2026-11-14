// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { NotebookPreview } from './NotebookPreview'

it('shows saved notebook cells without executing code or loading authored URLs', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<NotebookPreview content={JSON.stringify({ nbformat: 4, cells: [null, { cell_type: 'markdown', source: ['# Notes\n![remote](https://example.com/tracker)\n<script>alert(1)</script>'] }, { cell_type: 'code', source: 'print(1)', outputs: [{ data: { 'text/html': '<img src="https://example.com/leak">', 'application/javascript': 'alert(2)', 'application/vnd.jupyter.widget-view+json': { model_id: 'widget' } } }] }] })} />))
    expect(container.textContent).toContain('Notes')
    expect(container.textContent).toContain('print(1)')
    expect(container.textContent).toContain('https://example.com/leak')
    expect(container.textContent).toContain('alert(2)')
    expect(container.textContent).toContain('widget')
    expect(container.querySelector('img, script, iframe, a')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
