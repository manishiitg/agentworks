import { expect, it } from 'vitest'
import { remarkImageGallery, type GalleryNode } from './remarkImageGallery'

const image = (): GalleryNode => ({ type: 'paragraph', children: [{ type: 'image' }] })

// PLAT-746: blank lines between images should not require cards or lose intervening prose.
it('groups consecutive image paragraphs but never crosses prose, captions, code or headings', () => {
  const boundary: GalleryNode[] = [
    { type: 'paragraph', children: [{ type: 'text', value: 'Caption between images' }] },
    { type: 'paragraph', children: [{ type: 'image' }, { type: 'text', value: 'Inline explanation' }] },
    { type: 'heading', children: [{ type: 'text', value: 'Next section' }] },
    { type: 'code', value: '![example](image.png)' },
  ]
  for (const separator of boundary) {
    const tree: GalleryNode = { type: 'root', children: [image(), image(), separator, image(), image()] }
    remarkImageGallery()(tree)
    expect(tree.children).toHaveLength(3)
    expect(tree.children![0].children).toHaveLength(2)
    expect(tree.children![1]).toBe(separator)
    expect(tree.children![2].children).toHaveLength(2)
  }
})
