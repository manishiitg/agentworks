// Agent/imported CSS is parsed by the browser before inspecting normalized values.
// Do not allow CSS to send page contents via external resource selectors.
function localResources(value: string): string | null {
  if (value.includes('\\')) return null
  let unsafe = false
  // Treat a quoted data URL as one token: its SVG can contain url(#filter),
  // including URI-encoded fragment references. Those are not CSS fetches.
  const urls = /url\(\s*(?:"([^"]*)"|'([^']*)'|([^()\s"']*))\s*\)/gi
  const rest = value.replace(urls, (_match, double: string, single: string, bare: string) => {
    if (!/^(?:data:|#|%23)/i.test(double ?? single ?? bare)) unsafe = true
    return ''
  })
  if (unsafe || /(?:url|image-set|src)\s*\(/i.test(rest)) return null
  // In CSS itself, %23 is a URL path, not a fragment. Canonicalize it so an
  // encoded fragment can never cause a same-origin request.
  return value.replace(urls, (match, double: string, single: string, bare: string) => {
    const target = double ?? single ?? bare
    return /^%23/i.test(target) ? match.replace(/%23/i, '#') : match
  })
}
export function safeAppearanceSheet(css: string): { sheet: CSSStyleSheet; removed: boolean } {
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(css)
  let removed = /@import\b/i.test(css) // constructed sheets drop imports themselves
  const stripStyle = (rule: CSSRule): boolean => {
    if (!('style' in rule) || !(rule.style instanceof CSSStyleDeclaration)) return false
    const style = rule.style
    for (let i = style.length - 1; i >= 0; i--) {
      const name = style.item(i)
      const value = style.getPropertyValue(name)
      const local = localResources(value)
      if (local === null) { style.removeProperty(name); removed = true }
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
      else if (typeof CSSPropertyRule !== 'undefined' && rule instanceof CSSPropertyRule) { const value = rule.initialValue ?? ''; if (localResources(value) !== value) { parent.deleteRule(i); removed = true } }
      else if (!hasStyle && !(typeof CSSLayerStatementRule !== 'undefined' && rule instanceof CSSLayerStatementRule) && !(typeof CSSNamespaceRule !== 'undefined' && rule instanceof CSSNamespaceRule)) { parent.deleteRule(i); removed = true }
    }
  }
  clean(sheet)
  return { sheet, removed }
}
