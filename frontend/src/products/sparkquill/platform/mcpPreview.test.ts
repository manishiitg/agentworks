import { describe, expect, it } from 'vitest'
import { isSparkQuillPreviewPath } from './mcpPreview'
describe('SparkQuill MCP preview paths', () => {
  it('accepts family content links', () => {
    expect(isSparkQuillPreviewPath('activities/fractions/book.html')).toBe(true)
    expect(isSparkQuillPreviewPath('reports/progress.html')).toBe(true)
  })
  it('rejects traversal, private state and other workspace roots', () => {
    for (const path of ['https://evil.example', '/materials/photo.png', 'materials/../family.json', 'activities/book/product.json', 'activities/book/.env', 'materials\\photo.png', '_users/other/Chats/SparkQuill/materials/a.txt']) expect(isSparkQuillPreviewPath(path)).toBe(false)
  })
})
