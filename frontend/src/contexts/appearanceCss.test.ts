// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { safeAppearanceSheet } from './appearanceCss'

// Custom CSS may style the interface, but cannot use selectors to send visible
// data to an external server. Check parsed declarations, including escaped values.
it('drops external CSS resource declarations and keeps local styling', () => {
  const { sheet, removed } = safeAppearanceSheet(String.raw`
    [data-chat-composer-card] { border-radius: 20px; background: url(https://example.com/leak); --asset: u\72l(https://example.com/escaped); }
    [data-app-main] { background-image: url("data:image/svg+xml;base64,PHN2Zy8+"); }
    [data-app-ambient]::before {
      perspective: 700px; transform-style: preserve-3d; backface-visibility: hidden;
      transform: translate3d(1px,2px,3px) rotate3d(1,1,0,10deg);
      mask-image: linear-gradient(black, transparent); mix-blend-mode: screen;
      filter: drop-shadow(0 0 2px cyan);
    }
    @keyframes frame-tilt { to { transform: rotate3d(1,1,0,14deg); opacity: .5; } }
    @font-face { font-family: untrusted; src: url(https://example.com/font.woff2); }
    @supports (display: grid) { .example { color: red; background-image: image-set("https://example.com/a" 1x); } }
    .relative { mask: url(/private); filter: url(//example.com/filter); --relative: url(art.svg#filter); }
  `)
  expect(removed).toBe(true)
  const css = [...sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(css).not.toContain('example.com')
  expect(css).not.toContain('/private')
  expect(css).not.toContain('art.svg')
  expect(css).toContain('border-radius: 20px')
  expect(css).toContain('color: red')
  expect(css).toContain('data:image/svg+xml;base64,PHN2Zy8+')
  for (const supported of ['perspective:', 'preserve-3d', 'backface-visibility:', 'translate3d(', 'rotate3d(', 'mask-image:', 'mix-blend-mode:', 'drop-shadow(', '@keyframes frame-tilt']) expect(css).toContain(supported)
})

// PLAT-746-1040: embedded emblem SVG filters were misread as external CSS URLs,
// dropping the whole background and showing a false resource warning.
it('keeps embedded SVG fragment artwork and canonicalizes encoded CSS fragments', () => {
  const { sheet, removed } = safeAppearanceSheet(`
    [data-app-main] { --art: url("data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%3E%3Cg%20filter='url(%23tomoeGlow)'%20mask='url(%23tomoeCut)'/%3E%3C/svg%3E"); background-image: var(--art); }
    .emblem { filter: url(#tomoeGlow); mask: url('%23tomoeCut'); --fragment: url(%23another); }
  `)
  expect(removed).toBe(false)
  const css = [...sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(css).toContain('data:image/svg+xml,')
  expect(css).toContain("filter='url(%23tomoeGlow)'")
  expect(css).toContain("mask='url(%23tomoeCut)'")
  expect(css).toContain('filter: url(#tomoeGlow)')
  expect(css).toMatch(/url\(['"]?#tomoeCut['"]?\)/)
  expect(css).toMatch(/url\(['"]?#another['"]?\)/)
})
