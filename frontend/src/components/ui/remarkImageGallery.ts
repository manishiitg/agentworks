// Merge only adjacent image-only paragraphs; prose/headings/lists remain boundaries.
// This keeps ordinary Markdown sufficient for a gallery, without another agent format.
export type GalleryNode = { type: string; value?: string; children?: GalleryNode[] }
const imageOnly = (node: GalleryNode) => node.type === 'paragraph' &&
  node.children?.some(child => child.type === 'image' || child.type === 'imageReference') &&
  node.children.every(child => child.type === 'image' || child.type === 'imageReference' || child.type === 'break' || child.type === 'text' && !child.value?.trim())

export function remarkImageGallery() {
  return function visit(parent: GalleryNode): void {
    if (!parent.children) return
    const grouped: GalleryNode[] = []
    for (let index = 0; index < parent.children.length; index++) {
      const node = parent.children[index]
      if (imageOnly(node)) {
        const images = [...node.children!]
        while (index + 1 < parent.children.length && imageOnly(parent.children[index + 1])) {
          images.push(...parent.children[++index].children!)
        }
        grouped.push({ ...node, children: images.filter(child => child.type === 'image' || child.type === 'imageReference') })
      } else {
        visit(node)
        grouped.push(node)
      }
    }
    parent.children = grouped
  }
}
