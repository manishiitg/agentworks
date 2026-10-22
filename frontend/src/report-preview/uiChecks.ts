// Measurements for the existing preview, not a visual-design score. Reading
// the DOM never clicks controls or changes the authored dashboard.
export function inspectReportUI(doc: Document, viewportWidth: number) {
  const win = doc.defaultView
  if (!win || !doc.body) return { error: 'Dashboard document is unavailable.' }
  const frameWidth = doc.documentElement.clientWidth
  const contentWidth = Math.max(doc.documentElement.scrollWidth, doc.body.scrollWidth)
  const overflow = Math.max(0, contentWidth - frameWidth)
  const elements = Array.from(doc.body.querySelectorAll<HTMLElement>('*')).slice(0, 4000)
  const visible = (el: HTMLElement) => {
    const rect = el.getBoundingClientRect(), style = win.getComputedStyle(el)
    return rect.width > 0 && rect.height > 0 && style.visibility !== 'hidden' && style.display !== 'none'
  }
  const describe = (el: HTMLElement) => el.tagName.toLowerCase() + (el.id ? `#${el.id}` : '')
  const missingStylesheets = Array.from(doc.querySelectorAll<HTMLLinkElement>('link[rel~="stylesheet"]'))
    .filter(link => {
      const entry = win.performance.getEntriesByName(link.href).at(-1) as (PerformanceResourceTiming & { responseStatus?: number }) | undefined
      return !link.disabled && (!link.media || win.matchMedia(link.media).matches) && (!link.sheet || (entry?.responseStatus ?? 0) >= 400)
    })
    // URLs may carry preview credentials. Return only the resource pathname.
    .map(link => { try { return new URL(link.href, doc.baseURI).pathname } catch { return 'stylesheet' } })
  const overflowElements = overflow > 1 ? elements.filter(el => {
    if (!visible(el)) return false
    const rect = el.getBoundingClientRect()
    if (rect.left >= -1 && rect.right <= frameWidth + 1) return false
    for (let parent = el.parentElement; parent && parent !== doc.body; parent = parent.parentElement) {
      if (/auto|scroll|hidden|clip/.test(win.getComputedStyle(parent).overflowX)) return false
    }
    return true
  }).slice(0, 8).map(describe) : []
  const name = (el: HTMLElement) => {
    const references = (el.getAttribute('aria-labelledby') || '').split(/\s+/).map(id => doc.getElementById(id)?.textContent || '').join(' ')
    const labels = 'labels' in el ? Array.from((el as HTMLInputElement).labels || []).map(label => label.textContent).join(' ') : ''
    const value = el.tagName === 'INPUT' && /^(button|submit|reset)$/.test((el as HTMLInputElement).type) ? (el as HTMLInputElement).value : ''
    return [el.getAttribute('aria-label'), references, labels, el.getAttribute('title'), el.textContent, value,
      ...Array.from(el.querySelectorAll('img[alt]')).map(image => image.getAttribute('alt'))].filter(Boolean).join(' ').trim()
  }
  const unnamedControls = elements.filter(el => el.matches('button,input,select,textarea,[role="button"]') && visible(el) && !name(el)).slice(0, 8).map(describe)
  const brokenImages = elements.filter(el => el.tagName === 'IMG' && visible(el) && (el as HTMLImageElement).complete && (el as HTMLImageElement).naturalWidth === 0 && !!el.getAttribute('src')).length
  const smallText = elements.filter(el => visible(el) && el.children.length === 0 && !!el.textContent?.trim() && parseFloat(win.getComputedStyle(el).fontSize) < 12).length
  return {
    viewport_width: viewportWidth, frame_width: frameWidth, content_width: contentWidth,
    horizontal_overflow_px: overflow, overflow_elements: overflowElements,
    missing_stylesheets: missingStylesheets, stylesheet_count: doc.styleSheets.length,
    broken_images: brokenImages, unnamed_controls: unnamedControls, small_text_count: smallText,
    scanned_elements: elements.length, scan_truncated: doc.body.querySelectorAll('*').length > elements.length,
  }
}
