// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { actionForeground, appearanceTokens, applyAppearanceTokens } from './appThemeTokens'
import { defaultAppearance, validateTheme, APPEARANCE_CSS_MAX_BYTES } from '../stores/appearanceTheme'
import { changedLineRanges, themeViolations } from '../../scripts/check-theme-contract.mjs'
import { appearanceControlCSS } from './appearanceControls'

it('keeps action labels legible on light custom accents and propagates shared control colours', () => {
  const theme = defaultAppearance()
  theme.colors.dark = { accent: '#aaffdd', foreground: '#ddeeff', border: '#112233' }
  const tokens = appearanceTokens(theme, 'dark')
  expect(actionForeground('#aaffdd')).toBe('#000000')
  expect(actionForeground('#001122')).toBe('#ffffff')
  expect(tokens['--primary-foreground']).toBe('0 0% 0%')
  expect(tokens['--secondary-foreground']).toBe(tokens['--foreground'])
  expect(tokens['--input']).toBe(tokens['--border'])
  expect(tokens['--ring']).toBe(tokens['--primary'])
})

it('audits added style literals including variants without treating comments or authored palettes as app colours', () => {
  const source = '// bg-zinc-900\nconst x = `hover:bg-zinc-900 ${active ? "text-white" : "text-foreground"}`'
  expect(themeViolations(source, 'src/components/ui/Example.tsx').length).toBe(2)
  expect(themeViolations(source, 'src/components/ui/Example.tsx', new Set([1]))).toEqual([])
  expect(themeViolations(source, 'src/components/appearance/Scene.tsx')).toEqual([])
  const changed = changedLineRanges('+++ b/frontend/src/Example.tsx\n@@ -2,1 +2,2 @@\n-old\n+one\n+two')
  expect([...changed.get('frontend/src/Example.tsx')!]).toEqual([2,3])
})

// Shared material tokens remain independent of personal artwork.
it('projects surface materials and refuses a removed scene field', () => {
  const theme = defaultAppearance()
  expect(() => validateTheme({ ...theme, scene: 'deep-space' })).toThrow()
  theme.materials = { sidebar: { opacity: 0.85 }, panel: { opacity: 0.9, blur: 12 }, composer: { blur: 0 } }
  const tokens = appearanceTokens(theme, 'dark')
  expect([tokens['--aw-sidebar-opacity'], tokens['--aw-panel-opacity'], tokens['--aw-panel-backdrop'], tokens['--aw-composer-backdrop']])
    .toEqual(['0.85', '0.9', 'blur(12px)', 'none'])
})

// PLAT-746-1040: a personal loader must be cleared on account/theme changes;
// canonical validation must refuse unoffered variants instead of emitting CSS.
it('projects and clears personal loader and status roles without leaking the previous account', () => {
  const theme = defaultAppearance()
  theme.loader = { style: 'dots', duration_ms: 800 }
  theme.colors.dark = { working: '#123456', accent: '#eeeeff', waiting: '#654321', done: '#008800', error: '#aa0000', loader: '#abcdef' }
  expect(validateTheme(theme).loader).toEqual(theme.loader)
  const tokens = appearanceTokens(theme, 'dark')
  expect(tokens['--info']).toBeDefined()
  expect(tokens['--working-accent']).toBe(tokens['--info'])
  expect(tokens['--aw-loader-duration']).toBe('800ms')
  expect(tokens['--aw-loader-color']).toBe('#abcdef')
  expect(appearanceTokens(defaultAppearance(), 'dark')['--aw-loader-color']).toBeUndefined()
  const root = document.createElement('div')
  const cleanup = applyAppearanceTokens(root, theme, 'dark')
  expect(root.dataset.appearanceLoader).toBe('dots')
  cleanup()
  expect(root.dataset.appearanceLoader).toBeUndefined()
  expect(root.style.getPropertyValue('--aw-loader-color')).toBe('')
  for (const loader of [{ style: 'url(https://example.com)' }, { duration_ms: 0 }, { duration_ms: 4001 }, { duration_ms: 800.5 }, { script: 'example' }]) {
    expect(() => validateTheme({ ...theme, loader })).toThrow()
  }
  expect(validateTheme({ ...theme, loader: null }).loader).toBeNull()
})

// UTF-8 bytes, not character count; read uses the same advertised server cap.
it('accepts expressive CSS within the server byte cap and refuses larger Unicode input', () => {
  const theme = { ...defaultAppearance(), css: '/*' + '"<&雪'.repeat(16000) + '*/' }
  expect(validateTheme(theme).css).toBe(theme.css)
  expect(() => validateTheme(theme, { css_max_bytes: 20000 })).toThrow()
  expect(() => validateTheme({ ...theme, css: '雪'.repeat(Math.floor(APPEARANCE_CSS_MAX_BYTES / 3) + 1) })).toThrow()
})

// PLAT-746-1040: saved controls must not leak across an account/theme switch or
// into authored documents. Existing state hooks alone choose running borders.
it('keeps control isolation guards, existing state targets and owned sheet cleanup', () => {
  const theme = defaultAppearance()
  theme.icons = { style: 'square', stroke_width: 1.5, scale: 1.25 }
  theme.borders = { panel: { width_px: 2, radius_px: 8 }, card: { radius_px: 18 }, input: { style: 'dotted' }, selected_agent: { width_px: 1 }, running_agent: { width_px: 3 } }
  theme.colors.dark = { icon: '#123456', panel_border: '#654321' }
  expect(validateTheme(theme).borders?.card?.radius_px).toBe(18)
  const sheet = new CSSStyleSheet()
  sheet.replaceSync(appearanceControlCSS(theme, 'dark'))
  // Happy DOM's :is/:where/:not engine matches only the first compound of
  // nested descendant arguments (SelectorItem.matchPseudo). Check the parsed
  // guard contract here; actual descendant isolation needs a browser check.
  for (const rule of sheet.cssRules as unknown as CSSStyleRule[]) {
    expect(rule.selectorText).toMatch(/\.force-light\s+\*/)
    expect(rule.selectorText).toMatch(/\[data-report-theme\]\s+\*/)
    expect(rule.selectorText).toContain('[data-theme-surface="code"]')
    expect(rule.selectorText).toContain('[data-theme-surface="terminal"]')
  }
  const declarations = (element: Element) => [...sheet.cssRules].filter((rule): rule is CSSStyleRule => 'selectorText' in rule && element.matches((rule as CSSStyleRule).selectorText)).map(rule => rule.style)
  const root = document.documentElement, previous = root.dataset.appearance
  const fixture = document.createElement('div'); document.body.append(fixture)
  root.dataset.appearance = 'dark'
  try {
    const card = document.createElement('div'); card.dataset.themeCard = ''; card.dataset.themeSurface = 'panel'
    const panel = document.createElement('div'); panel.dataset.themeSurface = 'dialog'
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg'); svg.setAttribute('class', 'lucide')
    fixture.append(card, panel, svg)
    expect(declarations(card).at(-1)?.borderRadius).toBe('18px')
    expect(declarations(card).at(-1)?.borderWidth).toBe('2px')
    expect(declarations(panel)[0].borderRadius).toBe('8px')
    expect(declarations(svg)[0].getPropertyValue('stroke-width')).toBe('1.5')
    const row = document.createElement('div'); row.dataset.themeRow = 'agent'; row.dataset.themeAgentSelected = 'true'; fixture.append(row)
    expect(declarations(row).at(-1)?.borderWidth).toBe('1px')
    const mark = document.createElement('span'); mark.dataset.themeAgentState = 'running'; row.append(mark)
    expect(declarations(row).at(-1)?.borderWidth).toBe('3px')
    mark.remove()
    const historical = document.createElement('span'); historical.dataset.themeToolState = 'running'; row.append(historical)
    expect(declarations(row).at(-1)?.borderWidth).toBe('1px')
    const before = [...document.adoptedStyleSheets]
    const cleanup = applyAppearanceTokens(root, theme, 'dark')
    expect(document.adoptedStyleSheets).toHaveLength(before.length + 1)
    cleanup()
    expect(document.adoptedStyleSheets).toEqual(before)
    expect(appearanceControlCSS(defaultAppearance(), 'dark')).toBe('')
    for (const patch of [{ icons: { style: 'filled' } }, { icons: { scale: 0 } }, { icons: { stroke_width: '2px' } }, { borders: { input: { width_px: 1.5 } } }, { borders: { body: { style: 'solid' } } }, { borders: { card: { radius_px: -1 } } }]) expect(() => validateTheme({ ...theme, ...patch })).toThrow()
  } finally {
    fixture.remove()
    if (previous === undefined) delete root.dataset.appearance
    else root.dataset.appearance = previous
  }
})

// #2768: typed geometry must affect content, not virtualizer wrappers or actions;
// role styling and cleanup remain in the same personal sheet as icons/borders.
it('projects responsive chat styles and density without leaving the previous theme behind', () => {
  const theme = defaultAppearance()
  theme.chat = {user_style:'plain',assistant_style:'card',spacing_px:0,reading_width_px:760}
  theme.layout = {density:'compact',sidebar_width_px:280,right_panel_open:false}
  theme.colors.dark = {user_foreground:'#123456',assistant_background:'#eeeeff'}
  expect(validateTheme(theme).layout?.right_panel_open).toBe(false)
  const css=appearanceControlCSS(theme,'dark'), sheet=new CSSStyleSheet()
  sheet.replaceSync(css)
  const rules=[...sheet.cssRules].filter((r):r is CSSStyleRule=>'selectorText' in r)
  const reading=rules.find(r=>r.selectorText.includes('[data-theme-chat-row]'))!
  expect(reading.style.maxWidth).toBe('760px')
  expect(reading.style.width).toBe('100%')
  expect(reading.style.getPropertyValue('margin-inline')).toBe('auto')
  expect(css).not.toMatch(/data-virtuoso|overflow:|position:|display:none/)
  const user=rules.find(r=>r.selectorText.includes('role="user"')&&r.selectorText.includes('[data-theme-message-body]'))!
  const assistant=rules.find(r=>r.selectorText.includes('role="assistant"')&&r.selectorText.includes('[data-theme-message-body]'))!
  expect(user.style.backgroundColor).toBe('transparent')
  expect(user.style.color).toBe('#123456')
  expect(assistant.style.backgroundColor).toBe('#eeeeff')
  expect(assistant.style.borderRadius).toBe('8px')
  for(const r of rules) expect(r.selectorText).toContain('[data-report-theme]')
  expect(css).toContain('@media(min-width:768px)')
  const before=[...document.adoptedStyleSheets]
  const cleanup=applyAppearanceTokens(document.documentElement,theme,'dark')
  expect(document.adoptedStyleSheets).toHaveLength(before.length+1)
  cleanup()
  expect(document.adoptedStyleSheets).toEqual(before)
  expect(appearanceControlCSS(defaultAppearance(),'dark')).toBe('')
  expect(validateTheme({...theme,chat:null,layout:null}).chat).toBeNull()
  for(const patch of [{chat:{user_style:'hidden'}},{chat:{spacing_px:-1}},{chat:{reading_width_px:1601}},{chat:{spacing_px:1.5}},{layout:{sidebar_width_px:219}},{layout:{right_panel_open:'false'}},{layout:{density:'none'}}]) expect(()=>validateTheme({...theme,...patch})).toThrow()
})
