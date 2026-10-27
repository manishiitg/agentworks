// @vitest-environment happy-dom
import { act, useMemo } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useFilePreview, type PreviewContent } from './useFilePreview'

it('keeps an unchanged worksheet mounted through repeated opens and turn completion, but displays edits and new files', async () => {
  const pending: ((file: PreviewContent) => void)[] = []
  const read = vi.fn(() => new Promise<PreviewContent>((resolve) => pending.push(resolve)))
  const container = document.createElement('div')
  const root = createRoot(container)
  let current: PreviewContent | null = null
  function Viewer({ path, refresh }: { path: string; refresh: number }) {
    const file = useFilePreview(path, refresh, read)
    current = file
    const doc = useMemo(() => file?.content, [file])
    return file ? <iframe srcDoc={doc} /> : <p>Loading…</p>
  }
  const render = async (path: string, refresh: number) => act(async () => root.render(<Viewer path={path} refresh={refresh} />))
  const reply = async (content: string) => act(async () => pending.shift()!({ isText: true, content }))
  try {
    await render('test.html', 0)
    await reply('<input value="answer" />')
    const frame = container.querySelector('iframe')
    const original = current
    for (let refresh = 1; refresh <= 3; refresh++) {
      await render('test.html', refresh)
      expect(container.querySelector('iframe')).toBe(frame)
      await reply('<input value="answer" />')
      expect(container.querySelector('iframe')).toBe(frame)
      expect(current).toBe(original)
    }
    await render('test.html', 4)
    await reply('<p>Updated question</p>')
    expect(container.querySelector('iframe')?.getAttribute('srcdoc')).toBe('<p>Updated question</p>')
    await render('book.html', 5)
    expect(container.querySelector('iframe')).toBeNull()
    await reply('<p>A different page</p>')
    expect(container.querySelector('iframe')?.getAttribute('srcdoc')).toBe('<p>A different page</p>')
  } finally {
    await act(async () => root.unmount())
  }
})
