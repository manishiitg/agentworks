function inlineStyles(source: Element, clone: Element) {
  const style = getComputedStyle(source)
  const target = clone as HTMLElement | SVGElement
  for (const property of Array.from(style)) target.style.setProperty(property, style.getPropertyValue(property))
  // Keep arrow references local to the exported SVG document.
  for (const property of ['marker-start', 'marker-end']) {
    const reference = source.getAttribute(property)
    if (reference) target.style.setProperty(property, reference)
  }
  Array.from(source.children).forEach((child, index) => inlineStyles(child, clone.children[index]))
}

/** Capture the complete local diagram, beyond its scrolling viewport. */
export async function diagramPng(figure: HTMLElement, width: number, height: number): Promise<Blob> {
  await document.fonts?.ready
  const caption = figure.querySelector('figcaption')!
  const captionHeight = Math.ceil(caption.getBoundingClientRect().height)
  const style = getComputedStyle(figure)
  const borders = parseFloat(style.borderTopWidth || '0') + parseFloat(style.borderBottomWidth || '0')
  const exportWidth = Math.ceil(Math.max(figure.clientWidth, width) + borders)
  const exportHeight = Math.ceil(height + captionHeight + borders)
  const clone = figure.cloneNode(true) as HTMLElement
  inlineStyles(figure, clone)
  clone.setAttribute('xmlns', 'http://www.w3.org/1999/xhtml')
  Object.assign(clone.style, { width: `${exportWidth}px`, height: `${exportHeight}px`, maxWidth: 'none', margin: '0', overflow: 'visible' })
  clone.querySelectorAll('[data-diagram-export-controls], .sr-only').forEach(element => element.remove())
  const header = clone.querySelector<HTMLElement>('figcaption')!
  Object.assign(header.style, { width: '100%', height: `${captionHeight}px` })
  const viewport = clone.querySelector<HTMLElement>('[data-diagram-viewport]')!
  Object.assign(viewport.style, { width: '100%', height: `${height}px`, maxHeight: 'none', overflow: 'visible' })
  const html = new XMLSerializer().serializeToString(clone)
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${exportWidth}" height="${exportHeight}"><foreignObject width="100%" height="100%">${html}</foreignObject></svg>`
  const image = new Image()
  await new Promise<void>((resolve, reject) => {
    image.onload = () => resolve()
    image.onerror = () => reject(new Error('Diagram could not be rendered.'))
    image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
  })
  // Keep tall diagrams within canvas limits, with up to 2x resolution for text.
  const scale = Math.min(2, 8192 / exportWidth, 8192 / exportHeight, Math.sqrt(16_000_000 / (exportWidth * exportHeight)))
  const canvas = document.createElement('canvas')
  canvas.width = Math.max(1, Math.ceil(exportWidth * scale))
  canvas.height = Math.max(1, Math.ceil(exportHeight * scale))
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Diagram could not be rendered.')
  context.scale(scale, scale)
  context.fillStyle = `hsl(${style.getPropertyValue('--background').trim() || '0 0% 100%'})`
  context.fillRect(0, 0, exportWidth, exportHeight)
  context.drawImage(image, 0, 0)
  return new Promise((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('Diagram could not be rendered.')), 'image/png'))
}

export function downloadDiagram(blob: Blob, title: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `${title.replace(/[^\p{L}\p{N}-]+/gu, '-').slice(0, 80) || 'diagram'}.png`
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
