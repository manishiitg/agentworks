import { expect, it } from 'vitest'
import { actionForeground, appearanceTokens } from './appThemeTokens'
import { backgroundScene } from '../components/appearance/backgroundScenes'
import { defaultAppearance } from '../stores/appearanceTheme'
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

it('admits only shipped dark scenes and clears them for Light', () => {
  expect(backgroundScene(' deep-space ', 'dark')).toBe('deep-space')
  expect(backgroundScene('neon-orbit', 'dark')).toBe('neon-orbit')
  expect(backgroundScene('deep-space', 'light')).toBe('none')
  expect(backgroundScene('url(https://example.com/script)', 'dark')).toBe('none')
})

// The server's scene and materials reach the screen as the same --aw-* values custom CSS could set.
it('turns the saved scene and surface materials into the --aw tokens', () => {
  const theme = defaultAppearance()
  expect(appearanceTokens(theme, 'dark')['--aw-background']).toBeUndefined()
  theme.scene = 'deep-space'
  theme.materials = { sidebar: { opacity: 0.85 }, panel: { opacity: 0.9, blur: 12 }, composer: { blur: 0 } }
  const tokens = appearanceTokens(theme, 'dark')
  expect([tokens['--aw-background'], tokens['--aw-sidebar-opacity'], tokens['--aw-panel-opacity'], tokens['--aw-panel-backdrop'], tokens['--aw-composer-backdrop']])
    .toEqual(['deep-space', '0.85', '0.9', 'blur(12px)', 'none'])
})
