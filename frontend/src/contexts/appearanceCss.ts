import valueParser from 'postcss-value-parser'
import { parse } from 'postcss'

const maxCSSBytes = 128 << 10
const urls = /url\(\s*(?:"([^"]*)"|'([^']*)'|([^()\s"']*))\s*\)/gi
const https = (url: string) => /^https:\/\//i.test(url) && !/[\u0000-\u0020\u007f\\]/.test(url) && (() => {
  try { return new URL(url).protocol === 'https:' } catch { return false }
})()
const resource = (url: string) => https(url) || /^(?:data:image\/|data:font\/|data:application\/(?:font-|x-font-|vnd.ms-fontobject)|#|%23)/i.test(url)

// Inspect parsed URL and image-set tokens, not arbitrary strings in SVG data.
// Escaped resources stay refused, including escapes normalized only at use time
// through a custom property. The fragment canonicalization is PR2716's rule.
function safeResources(value: string): string | null {
  if (value.includes('\\')) return null
  let unsafe = false
  const parsed = valueParser(value)
  parsed.walk(node => {
    if (node.type !== 'function') return
    const name = node.value.toLowerCase()
    if (name === 'url' || name === 'src') {
      const target = valueParser.stringify(node.nodes).trim().replace(/^(?:"(.*)"|'(.*)')$/, '$1$2')
      if (!resource(target)) unsafe = true
      return false // An embedded SVG is an opaque URL, not more CSS.
    }
    if (name === 'image-set' || name === '-webkit-image-set' || name === 'image') {
      for (const item of node.nodes) {
        if (item.type === 'string' && !resource(item.value)) unsafe = true
        if (item.type === 'function' && item.value.toLowerCase() === 'var') unsafe = true
      }
    }
    if (name === 'expression') unsafe = true
  })
  if (unsafe) return null
  return value.replace(urls, (match, double: string, single: string, bare: string) => {
    return /^%23/i.test(double ?? single ?? bare) ? match.replace(/%23/i, '#') : match
  })
}

type FontImport = { url: string; media: string }
function splitImports(css: string): { css: string; imports: FontImport[]; removed: boolean } {
  const root = parse(css, { from: undefined })
  const imports: FontImport[] = []
  let removed = false
  root.walkAtRules(/^import$/i, rule => {
    const tokens = valueParser(rule.params).nodes
    const firstIndex = tokens.findIndex(node => node.type !== 'space' && node.type !== 'comment')
    const first = tokens[firstIndex]
    const url = first?.type === 'string' ? first.value : first?.type === 'function' && first.value.toLowerCase() === 'url' ? valueParser.stringify(first.nodes).trim().replace(/^(?:"(.*)"|'(.*)')$/, '$1$2') : ''
    const media = valueParser.stringify(tokens.slice(firstIndex + 1)).trim()
    // Font CSS imports support an optional media query, not arbitrary rule text.
    if (rule.parent === root && https(url) && !/[{};\\]/.test(media) && !/\b(?:url|layer|supports)\s*\(/i.test(media)) imports.push({ url, media })
    else removed = true
    rule.remove()
  })
  return { css: root.toString(), imports, removed }
}

export function safeAppearanceSheet(css: string): { sheet: CSSStyleSheet; removed: boolean; imports: FontImport[] } {
  if (new TextEncoder().encode(css).length > maxCSSBytes) throw new Error('Custom CSS exceeds 128 KiB')
  const input = splitImports(css)
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(input.css)
  let removed = input.removed
  const stripStyle = (rule: CSSRule): boolean => {
    if (!('style' in rule) || !(rule.style instanceof CSSStyleDeclaration)) return false
    const style = rule.style
    for (let i = style.length - 1; i >= 0; i--) {
      const name = style.item(i)
      const value = style.getPropertyValue(name)
      const local = safeResources(value)
      if (local === null || name === 'behavior' || name === '-moz-binding' || hidesControl(name, value, rule)) { style.removeProperty(name); removed = true }
      else if (local !== value) style.setProperty(name, local, style.getPropertyPriority(name))
    }
    return true
  }
  const clean = (parent: Pick<CSSStyleSheet, 'cssRules' | 'deleteRule'>) => {
    for (let i = parent.cssRules.length - 1; i >= 0; i--) {
      const rule = parent.cssRules[i]
      const hasStyle = stripStyle(rule)
      if (rule instanceof CSSKeyframesRule) {
        for (const frame of rule.cssRules) stripStyle(frame)
      } else if ('cssRules' in rule && 'deleteRule' in rule) clean(rule as CSSGroupingRule)
      else if (typeof CSSPropertyRule !== 'undefined' && rule instanceof CSSPropertyRule) { const value = rule.initialValue ?? ''; if (safeResources(value) !== value) { parent.deleteRule(i); removed = true } }
      else if (!hasStyle && !(typeof CSSLayerStatementRule !== 'undefined' && rule instanceof CSSLayerStatementRule) && !(typeof CSSNamespaceRule !== 'undefined' && rule instanceof CSSNamespaceRule)) { parent.deleteRule(i); removed = true }
    }
  }
  clean(sheet)
  return { sheet, removed, imports: input.imports }
}

// Decoration may be noninteractive; ordinary rules must not remove/disable UI.
// This is a guard against the common hiding primitives, not a CSS sandbox.
function hidesControl(name: string, value: string, rule: CSSRule): boolean {
  const selector = 'selectorText' in rule ? String(rule.selectorText) : ''
  if (selector && selector.split(',').every(part => /::(?:before|after)\s*$/.test(part.trim()))) return false
  const v = value.trim().toLowerCase()
  return name === 'display' && v === 'none' || name === 'visibility' && /^(hidden|collapse)$/.test(v) ||
    name === 'content-visibility' && v === 'hidden' || name === 'pointer-events' && v === 'none' ||
    name === 'opacity' && Number(v) === 0 || name === 'interactivity' && v === 'inert'
}

// Constructed sheets drop @import. Fetch font stylesheets explicitly, then apply
// the SAME URL checks. Remote sheets may supply fonts, never app selectors.
// Credentials/referrers are omitted; relative URLs are not rebased into permission.
export async function loadAppearanceFonts(imports: FontImport[], signal: AbortSignal): Promise<{ sheet: CSSStyleSheet; removed: boolean }> {
  const seen = new Set<string>()
  let bytes = 0
  let removed = false
  const load = async (entry: FontImport, depth: number): Promise<string> => {
    if (depth > 3 || seen.size >= 8 || seen.has(entry.url)) { removed = true; return '' }
    seen.add(entry.url)
    try {
      const response = await fetch(entry.url, { signal, credentials: 'omit', referrerPolicy: 'no-referrer', redirect: 'error' })
      if (!response.ok || !https(response.url || entry.url) || !response.headers.get('content-type')?.toLowerCase().includes('text/css')) throw new Error('Font CSS unavailable')
      const reader = response.body?.getReader()
      if (!reader) throw new Error('Font CSS unavailable')
      const decoder = new TextDecoder()
      let text = ''
      try {
        while (true) {
          const result = await reader.read()
          if (result.done) break
          bytes += result.value.byteLength
          if (bytes > maxCSSBytes) { await reader.cancel(); throw new Error('Font CSS exceeds limit') }
          text += decoder.decode(result.value, { stream: true })
        }
        text += decoder.decode()
      } finally { reader.releaseLock() }
      const root = parse(text, { from: undefined })
      root.each(node => {
        if (node.type === 'comment' || node.type === 'atrule' && /^(font-face|import)$/i.test(node.name)) return
        node.remove(); removed = true
      })
      const safe = safeAppearanceSheet(root.toString())
      removed ||= safe.removed
      const nested: string[] = []
      for (const item of safe.imports) nested.push(await load(item, depth + 1))
      const css = nested.join('\n') + '\n' + [...safe.sheet.cssRules].map(rule => rule.cssText).join('\n')
      return entry.media ? `@media ${entry.media} {${css}}` : css
    } catch (error) {
      if (signal.aborted) throw error
      removed = true
      return ''
    }
  }
  const css: string[] = []
  for (const entry of imports) css.push(await load(entry, 0))
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(css.join('\n'))
  return { sheet, removed }
}
