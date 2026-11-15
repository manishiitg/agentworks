// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { actionForeground, appearanceTokens, applyAppearanceTokens } from './appThemeTokens'
import { defaultAppearance, validateTheme, APPEARANCE_CSS_MAX_BYTES } from '../stores/appearanceTheme'
import { changedLineRanges, themeViolations } from '../../scripts/check-theme-contract.mjs'

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
  const theme = { ...defaultAppearance(), css: '/*' + '"<&雪'.repeat(8000) + '*/' }
  expect(validateTheme(theme).css).toBe(theme.css)
  expect(() => validateTheme(theme, { css_max_bytes: 20000 })).toThrow()
  expect(() => validateTheme({ ...theme, css: '雪'.repeat(Math.floor(APPEARANCE_CSS_MAX_BYTES / 3) + 1) })).toThrow()
})
