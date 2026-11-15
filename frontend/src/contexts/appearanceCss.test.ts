// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { safeAppearanceSheet } from './appearanceCss'

// Custom CSS may style the interface, but cannot use selectors to send visible
// data to an external server. Check parsed declarations, including escaped values.
it('drops external CSS resource declarations and keeps local styling', () => {
  const { sheet, removed } = safeAppearanceSheet(String.raw`
    [data-chat-composer-card] { border-radius: 20px; background: url(https://example.com/leak); --asset: u\72l(https://example.com/escaped); }
    @font-face { font-family: untrusted; src: url(https://example.com/font.woff2); }
    @supports (display: grid) { .example { color: red; background-image: image-set("https://example.com/a" 1x); } }
  `)
  expect(removed).toBe(true)
  const css = [...sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(css).not.toContain('example.com')
  expect(css).toContain('border-radius: 20px')
  expect(css).toContain('color: red')
})
