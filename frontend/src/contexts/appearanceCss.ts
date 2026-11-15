// Agent/imported CSS is parsed by the browser before inspecting normalized values.
// Do not allow CSS to send page contents via external resource selectors.
const fetches = (value: string) => value.includes('\\') || /(?:image-set|src)\s*\(/i.test(value) || /url\((?![ \t\n\r\f]*["']?(?:data:|#))/i.test(value)
export function safeAppearanceSheet(css: string): { sheet: CSSStyleSheet; removed: boolean } {
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(css)
  let removed = /@import\b/i.test(css) // constructed sheets drop imports themselves
  const clean = (parent: Pick<CSSStyleSheet, 'cssRules' | 'deleteRule'>) => {
    for (let i = parent.cssRules.length - 1; i >= 0; i--) {
      const rule = parent.cssRules[i]
      if ('style' in rule && rule.style instanceof CSSStyleDeclaration) {
        const style = rule.style
        for (let n = style.length - 1; n >= 0; n--) { const name = style.item(n); if (fetches(style.getPropertyValue(name))) { style.removeProperty(name); removed = true } }
      }
      if (rule instanceof CSSKeyframesRule) {
        for (const frame of rule.cssRules) for (let n = frame.style.length - 1; n >= 0; n--) { const name = frame.style.item(n); if (fetches(frame.style.getPropertyValue(name))) { frame.style.removeProperty(name); removed = true } }
      } else if ('cssRules' in rule && 'deleteRule' in rule) clean(rule as CSSGroupingRule)
      else if (typeof CSSPropertyRule !== 'undefined' && rule instanceof CSSPropertyRule) { if (fetches(rule.initialValue ?? '')) { parent.deleteRule(i); removed = true } }
      else if (!('style' in rule) && !(typeof CSSLayerStatementRule !== 'undefined' && rule instanceof CSSLayerStatementRule) && !(typeof CSSNamespaceRule !== 'undefined' && rule instanceof CSSNamespaceRule)) { parent.deleteRule(i); removed = true }
    }
  }
  clean(sheet)
  return { sheet, removed }
}
