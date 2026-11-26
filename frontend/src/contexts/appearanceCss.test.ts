// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { loadAppearanceFonts, safeAppearanceSheet } from './appearanceCss'

// PLAT-746-1040: HTTPS artwork/fonts are an explicit owner choice. Relative,
// script and escaped fetches must not gain permission through that choice.
it('allows HTTPS images and fonts but drops unsafe URLs and hiding controls', () => {
  const { sheet, dropped } = safeAppearanceSheet(String.raw`
    [data-chat-composer-card] { border-radius: 20px; background: url(https://example.com/art.png); --asset: u\72l(https://example.com/escaped); }
    [data-app-main] { background-image: url("data:image/svg+xml;base64,PHN2Zy8+"); }
    [data-app-ambient]::before {
      perspective: 700px; transform-style: preserve-3d; backface-visibility: hidden;
      transform: translate3d(1px,2px,3px) rotate3d(1,1,0,10deg);
      mask-image: linear-gradient(black, transparent); mix-blend-mode: screen;
      filter: drop-shadow(0 0 2px cyan);
    }
    @keyframes frame-tilt { to { transform: rotate3d(1,1,0,14deg); opacity: .5; } }
    @font-face { font-family: custom; src: url(https://example.com/font.woff2); }
    @supports (display: grid) { .example { color: red; background-image: image-set("https://example.com/a" 1x); } }
    .relative { mask: url(/private); filter: url(//example.com/filter); --relative: url(art.svg#filter); }
    .script { background-image: url("javascript:alert(1)"); --script: expression(alert(1)); }
    .relative-set { background-image: image-set("relative.png" 1x); }
    .variable-set { --path: "relative.png"; background-image: image-set(var(--path) 1x); }
    button { display: none; visibility: hidden; pointer-events: none; opacity: 0; }
    [data-app-ambient]::after { pointer-events: none; opacity: 0; }
  `)
  expect(dropped.length).toBeGreaterThan(0)
  const css = [...sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(css).toContain('https://example.com/art.png')
  expect(css).toContain('https://example.com/a')
  expect(css).toContain('https://example.com/font.woff2')
  expect(css).not.toContain('example.com/escaped')
  expect(css).not.toContain('javascript:')
  expect(css).not.toContain('expression(')
  expect(css).not.toContain('image-set("relative.png"')
  expect(css).not.toContain('image-set(var(')
  const controls = [...sheet.cssRules].find(rule => 'selectorText' in rule && rule.selectorText === 'button') as CSSStyleRule
  expect(controls.style.length).toBe(0)
  const decoration = [...sheet.cssRules].find(rule => 'selectorText' in rule && rule.selectorText === '[data-app-ambient]::after') as CSSStyleRule
  expect(decoration.style.getPropertyValue('pointer-events')).toBe('none')
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
  const { sheet, dropped } = safeAppearanceSheet(`
    [data-app-main] { --art: url("data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%3E%3Cg%20filter='url(%23tomoeGlow)'%20mask='url(%23tomoeCut)'/%3E%3C/svg%3E"); background-image: var(--art); }
    .emblem { filter: url(#tomoeGlow); mask: url('%23tomoeCut'); --fragment: url(%23another); --fragment-set: image-set("%23emblem" 1x); }
  `)
  expect(dropped).toEqual([])
  const css = [...sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(css).toContain('data:image/svg+xml,')
  expect(css).toContain("filter='url(%23tomoeGlow)'")
  expect(css).toContain("mask='url(%23tomoeCut)'")
  expect(css).toContain('filter: url(#tomoeGlow)')
  expect(css).toMatch(/url\(['"]?#tomoeCut['"]?\)/)
  expect(css).toMatch(/url\(['"]?#another['"]?\)/)
  expect(css).toContain('image-set("#emblem" 1x)')
})

// #3085: attribute selectors on [value] let CSS read what a person types; the
// rule is dropped and listed (with the other skipped pieces) for the agent.
it('drops rules styled by an input value and lists every dropped piece with a reason', () => {
  const { sheet, dropped } = safeAppearanceSheet(`
    input[value^="a"] { background: url(https://example.com/a); }
    @media screen { input[VALUE$="z"] { color: red; } }
    [data-value="x"] { color: blue; }
    .ok { background: url(https://example.com/ok.png); }
    .bad { background: url(/private); }
    @import "relative.css";
  `)
  const css = [...sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(css).not.toMatch(/\[\s*value/i)
  expect(css).toContain('[data-value="x"]')
  expect(css).toContain('https://example.com/ok.png')
  expect(dropped).toHaveLength(4)
  for (const item of dropped) expect(item.reason).toBeTruthy()
  expect(dropped.some(item => item.text.includes('/private'))).toBe(true)
})

afterEach(() => vi.unstubAllGlobals())

// Constructed sheets silently drop imports: exercise the actual font loader,
// including re-sanitization and rejecting remote UI rules, not just URL parsing.
it('loads HTTPS font CSS without credentials and keeps relative imports and remote UI rules out', async () => {
  const controller = new AbortController()
  const fetcher = vi.fn(async () => new Response(`
    @import "relative.css";
    @font-face { font-family: Imported; src: url(https://example.com/face.woff2) format("woff2"); }
    @font-face { font-family: Refused; src: url("javascript:alert(1)"); }
    button { display: none; }
  `, { headers: { 'content-type': 'text/css; charset=utf-8' } }))
  vi.stubGlobal('fetch', fetcher)
  const local = safeAppearanceSheet(`
    @import url("https://example.com/fonts.css") screen and (min-width: 1px);
    @import "//example.com/no.css";
    @import "javascript:alert(1)";
    @import "relative.css";
    .local { color: red; }
  `)
  expect(local.dropped).toHaveLength(3)
  expect(local.imports).toEqual([{ url: 'https://example.com/fonts.css', media: 'screen and (min-width: 1px)' }])
  const result = await loadAppearanceFonts(local.imports, controller.signal)
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(fetcher).toHaveBeenCalledWith('https://example.com/fonts.css', { signal: controller.signal, credentials: 'omit', referrerPolicy: 'no-referrer', redirect: 'error' })
  const css = [...result.sheet.cssRules].map(rule => rule.cssText).join('\n')
  expect(result.dropped.length).toBeGreaterThan(0)
  expect(css).toContain('https://example.com/face.woff2')
  expect(css).toContain('@media screen and (min-width: 1px)')
  expect(css).not.toContain('javascript:')
  expect(css).not.toContain('relative.css')
  expect(css).not.toContain('button')
  expect([...local.sheet.cssRules].map(rule => rule.cssText).join('')).toContain('color: red')
  // A failed import does not discard local styles or adopt an unchecked sheet.
  fetcher.mockRejectedValueOnce(new Error('CORS unavailable'))
  expect((await loadAppearanceFonts(local.imports, controller.signal)).dropped.length).toBeGreaterThan(0)
})
