import valueParser from 'postcss-value-parser'
import { parse } from 'postcss'

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
      const token = node.nodes.find(item => item.type !== 'space' && item.type !== 'comment')
      if (token && (token.type === 'word' || token.type === 'string')) token.value = token.value.replace(/^%23/i, '#')
      return false // An embedded SVG is an opaque URL, not more CSS.
    }
    if (name === 'image-set' || name === '-webkit-image-set' || name === 'image') {
      for (const item of node.nodes) {
        if (item.type === 'string') {
          if (!resource(item.value)) unsafe = true
          item.value = item.value.replace(/^%23/i, '#')
        }
        if (item.type === 'function' && item.value.toLowerCase() === 'var') unsafe = true
      }
    }
    if (name === 'expression') unsafe = true
  })
  if (unsafe) return null
  return parsed.toString()
}

/** One piece of theme CSS the app did not apply, with a short reason the agent can read. */
export type DroppedCSS = { text: string; reason: string }
const MAX_DROPPED = 50
// Remote font stylesheets are fetched by the browser: bound what one theme can make it download.
const FONT_CSS_MAX_BYTES = 128 * 1024

function dropList() {
  const list: DroppedCSS[] = []
  return {
    list,
    add: (text: string, reason: string) => {
      if (list.length < MAX_DROPPED) list.push({ text: text.replace(/\s+/g, ' ').trim().slice(0, 300), reason })
    },
  }
}

type FontImport = { url: string; media: string }
function splitImports(css: string, drop: ReturnType<typeof dropList>): { css: string; imports: FontImport[] } {
  const root = parse(css, { from: undefined })
  const imports: FontImport[] = []
  root.walkAtRules(/^import$/i, rule => {
    const tokens = valueParser(rule.params).nodes
    const firstIndex = tokens.findIndex(node => node.type !== 'space' && node.type !== 'comment')
    const first = tokens[firstIndex]
    let url = ''
    if (first?.type === 'string') url = first.value
    else if (first?.type === 'function' && first.value.toLowerCase() === 'url') url = valueParser.stringify(first.nodes).trim().replace(/^(?:"(.*)"|'(.*)')$/, '$1$2')
    const media = valueParser.stringify(tokens.slice(firstIndex + 1)).trim()
    // Font CSS imports support an optional media query, not arbitrary rule text.
    const mediaOK = !/[{};\\]/.test(media) && !/\b(?:url|layer|supports)\s*\(/i.test(media)
    if (rule.parent === root && https(url) && mediaOK) imports.push({ url, media })
    else drop.add(`@import ${rule.params}`, rule.parent !== root ? 'imports must be at the top level' : https(url) ? 'imports take only a media query' : 'imports need a full https:// address')
    rule.remove()
  })
  return { css: root.toString(), imports }
}

// Styling by an input's value lets CSS read what a person types (attribute
// selectors plus background URLs). The server refuses these too.
const valueSelector = /\[\s*value\b/i

export function safeAppearanceSheet(css: string): { sheet: CSSStyleSheet; dropped: DroppedCSS[]; imports: FontImport[] } {
  const drop = dropList()
  const input = splitImports(css, drop)
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(input.css)
  const stripStyle = (rule: CSSRule): boolean => {
    if (!('style' in rule) || !(rule.style instanceof CSSStyleDeclaration)) return false
    const style = rule.style
    for (let i = style.length - 1; i >= 0; i--) {
      const name = style.item(i)
      const value = style.getPropertyValue(name)
      const local = safeResources(value)
      const reason = local === null ? 'only https:// and data: images and fonts are allowed'
        : name === 'behavior' || name === '-moz-binding' ? 'this property can run code'
        : hidesControl(name, value, rule) ? 'it would hide or disable app controls (allowed on ::before/::after)'
        : ''
      if (reason) {
        drop.add(`${selectorOf(rule)} { ${name}: ${value} }`, reason)
        style.removeProperty(name)
      } else if (local !== value) style.setProperty(name, local, style.getPropertyPriority(name))
    }
    return true
  }
  const clean = (parent: Pick<CSSStyleSheet, 'cssRules' | 'deleteRule'>) => {
    for (let i = parent.cssRules.length - 1; i >= 0; i--) {
      const rule = parent.cssRules[i]
      if ('selectorText' in rule && valueSelector.test(String(rule.selectorText))) {
        drop.add(rule.cssText, "styles by an input's value")
        parent.deleteRule(i)
        continue
      }
      const hasStyle = stripStyle(rule)
      if (rule instanceof CSSKeyframesRule) {
        for (const frame of rule.cssRules) stripStyle(frame)
      } else if ('cssRules' in rule && 'deleteRule' in rule) {
        clean(rule as CSSGroupingRule)
      } else if (typeof CSSPropertyRule !== 'undefined' && rule instanceof CSSPropertyRule) {
        const value = rule.initialValue ?? ''
        if (safeResources(value) !== value) {
          drop.add(rule.cssText, 'only https:// and data: images and fonts are allowed')
          parent.deleteRule(i)
        }
      } else if (!hasStyle && !isLayerStatement(rule) && !isNamespace(rule)) {
        drop.add(rule.cssText, 'this kind of rule is not supported')
        parent.deleteRule(i)
      }
    }
  }
  clean(sheet)
  return { sheet, dropped: drop.list, imports: input.imports }
}

const isLayerStatement = (rule: CSSRule) => typeof CSSLayerStatementRule !== 'undefined' && rule instanceof CSSLayerStatementRule
const isNamespace = (rule: CSSRule) => typeof CSSNamespaceRule !== 'undefined' && rule instanceof CSSNamespaceRule
const selectorOf = (rule: CSSRule) => 'selectorText' in rule ? String(rule.selectorText) : 'keyText' in rule ? String(rule.keyText) : rule.cssText.split('{')[0].trim()

// Decoration may be noninteractive; ordinary rules must not remove/disable UI.
// This is a guard against the common hiding primitives, not a CSS sandbox.
function hidesControl(name: string, value: string, rule: CSSRule): boolean {
  const selector = 'selectorText' in rule ? String(rule.selectorText) : ''
  if (selector && selector.split(',').every(part => /::(?:before|after)\s*$/.test(part.trim()))) return false
  const v = value.trim().toLowerCase()
  return (name === 'display' && v === 'none')
    || (name === 'visibility' && /^(hidden|collapse)$/.test(v))
    || (name === 'content-visibility' && v === 'hidden')
    || (name === 'pointer-events' && v === 'none')
    || (name === 'opacity' && Number(v) === 0)
    || (name === 'interactivity' && v === 'inert')
}

// Constructed sheets drop @import. Fetch font stylesheets explicitly, then apply
// the SAME URL checks. Remote sheets may supply fonts, never app selectors.
// Credentials/referrers are omitted; relative URLs are not rebased into permission.
export async function loadAppearanceFonts(imports: FontImport[], signal: AbortSignal): Promise<{ sheet: CSSStyleSheet; dropped: DroppedCSS[] }> {
  const seen = new Set<string>()
  const drop = dropList()
  let bytes = 0
  const load = async (entry: FontImport, depth: number): Promise<string> => {
    if (depth > 3 || seen.size >= 8 || seen.has(entry.url)) {
      drop.add(`@import "${entry.url}"`, 'too many or repeated font stylesheets')
      return ''
    }
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
          if (bytes > FONT_CSS_MAX_BYTES) {
            await reader.cancel()
            throw new Error('Font CSS exceeds limit')
          }
          text += decoder.decode(result.value, { stream: true })
        }
        text += decoder.decode()
      } finally {
        reader.releaseLock()
      }
      const root = parse(text, { from: undefined })
      root.each(node => {
        if (node.type === 'comment' || (node.type === 'atrule' && /^(font-face|import)$/i.test(node.name))) return
        drop.add(`${entry.url}: ${node.toString()}`, 'font stylesheets may only contain @font-face')
        node.remove()
      })
      const safe = safeAppearanceSheet(root.toString())
      for (const item of safe.dropped) drop.add(`${entry.url}: ${item.text}`, item.reason)
      const nested: string[] = []
      for (const item of safe.imports) nested.push(await load(item, depth + 1))
      const css = nested.join('\n') + '\n' + [...safe.sheet.cssRules].map(rule => rule.cssText).join('\n')
      return entry.media ? `@media ${entry.media} {${css}}` : css
    } catch (error) {
      if (signal.aborted) throw error
      drop.add(`@import "${entry.url}"`, 'the font stylesheet could not be loaded (it must be https, text/css, without redirects)')
      return ''
    }
  }
  const css: string[] = []
  for (const entry of imports) css.push(await load(entry, 0))
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(css.join('\n'))
  return { sheet, dropped: drop.list }
}
