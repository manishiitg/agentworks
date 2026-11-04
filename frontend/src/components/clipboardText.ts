// A textarea stores Markdown. Preserve useful clipboard formatting without
// rendering or running the copied HTML in the app (PLAT-746-309).
export function getClipboardText(clipboard: Pick<DataTransfer, 'getData'>): string {
  const plain = clipboard.getData('text/plain') || clipboard.getData('text')
  const html = clipboard.getData('text/html')
  if (!html || typeof DOMParser === 'undefined') return plain
  const doc = new DOMParser().parseFromString(html, 'text/html')
  const hasStyledText = Array.from(doc.body.querySelectorAll<HTMLElement>('[style]')).some(({ style }) =>
    style.fontWeight === 'bold' || Number(style.fontWeight) >= 600 || style.fontStyle === 'italic' ||
    style.textDecorationLine.includes('line-through') || style.textDecoration.includes('line-through'))
  if (!hasStyledText && !doc.body.querySelector('b,strong,i,em,s,strike,del,code,pre,blockquote,ul,ol,a[href],h1,h2,h3,h4,h5,h6')) return plain
  const escape = (text: string) => text.replace(/([\\`*_\[\]~])/g, '\\$1')
  const children = (node: Node): string => Array.from(node.childNodes).map(render).join('')
  const fenced = (text: string, block: boolean) => {
    const longest = Math.max(0, ...Array.from(text.matchAll(/`+/g), match => match[0].length))
    const fence = '`'.repeat(Math.max(block ? 3 : 1, longest + 1))
    return block ? `\n\n${fence}\n${text.replace(/\n$/, '')}\n${fence}\n\n` : `${fence} ${text} ${fence}`
  }
  function render(node: Node): string {
    if (node.nodeType === Node.TEXT_NODE) return escape((node.textContent || '').replace(/[\t\r\n ]+/g, ' ').replace(/\u00a0/g, ' '))
    if (!(node instanceof Element)) return ''
    const tag = node.tagName.toLowerCase()
    if (['script', 'style', 'noscript', 'template'].includes(tag)) return ''
    if (tag === 'br') return '\n'
    if (tag === 'pre') return fenced(node.textContent || '', true)
    if (tag === 'code') return fenced(node.textContent || '', false)
    if (tag === 'ul' || tag === 'ol') {
      const start = Number(node.getAttribute('start')) || 1
      const items = Array.from(node.children).filter(child => child.tagName === 'LI')
      return '\n\n' + items.map((item, index) => {
        const prefix = tag === 'ol' ? `${start + index}. ` : '- '
        const text = children(item).trim().replace(/\n{3,}/g, '\n\n')
        return prefix + text.replace(/\n/g, '\n' + ' '.repeat(prefix.length))
      }).join('\n') + '\n\n'
    }
    let text = children(node)
    if (!text.trim()) return text
    if (tag === 'blockquote') return '\n\n' + text.trim().split('\n').map(line => '> ' + line).join('\n') + '\n\n'
    if (/^h[1-6]$/.test(tag)) return '\n\n' + '#'.repeat(Number(tag[1])) + ' ' + text.trim() + '\n\n'
    if (tag === 'a') {
      const href = node.getAttribute('href') || ''
      if (/^(https?:|mailto:)/i.test(href)) text = `[${text.trim()}](<${href.replace(/>/g, '%3E').replace(/</g, '%3C').replace(/[\r\n]/g, '')}>)`
    }
    const style = (node as HTMLElement).style
    const bold = tag === 'b' || tag === 'strong' || style?.fontWeight === 'bold' || Number(style?.fontWeight) >= 600
    const italic = tag === 'i' || tag === 'em' || style?.fontStyle === 'italic'
    const strike = ['s', 'strike', 'del'].includes(tag) || style?.textDecorationLine?.includes('line-through') || style?.textDecoration?.includes('line-through')
    // Keep surrounding spaces outside Markdown delimiters.
    const wrap = (marker: string) => { text = text.replace(/^(\s*)([\s\S]*?)(\s*)$/, (_, before, body, after) => `${before}${marker}${body}${marker}${after}`) }
    if (bold) wrap('**')
    if (italic) wrap('*')
    if (strike) wrap('~~')
    if (tag === 'p' || tag === 'div') return '\n\n' + text.trim() + '\n\n'
    return text
  }
  const markdown = children(doc.body).replace(/\n[ \t]+\n/g, '\n\n').replace(/\n{3,}/g, '\n\n').trim()
  return markdown || plain
}
