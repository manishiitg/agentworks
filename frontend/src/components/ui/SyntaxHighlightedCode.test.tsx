// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import { ThemeContext, type Theme } from '../../contexts/ThemeContext'
import SyntaxHighlightedCode from './SyntaxHighlightedCode'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('SyntaxHighlightedCode theme reactivity', () => {
  it('follows ThemeContext when switching to dark mode and keeps inner code background transparent', async () => {
    const host = document.createElement('div')
    const root = createRoot(host)
    const renderWith = (theme: Theme) => (
      <ThemeContext.Provider value={{ theme, toggleTheme: () => {}, setTheme: () => {} }}>
        <SyntaxHighlightedCode code="git checkout main && git merge --ff-only origin/main" language="bash" isDark={false} />
      </ThemeContext.Provider>
    )
    try {
      await act(async () => root.render(renderWith('light')))
      const lightPre = host.querySelector<HTMLElement>('[data-theme-surface="code"]')
      expect(lightPre?.style.background).toMatch(/#f9fafb|rgb\(249,\s*250,\s*251\)/i)

      await act(async () => root.render(renderWith('dark')))
      const darkPre = host.querySelector<HTMLElement>('[data-theme-surface="code"]')
      const codeEl = darkPre?.querySelector<HTMLElement>('code')
      expect(darkPre?.style.background).toMatch(/#1f2937|rgb\(31,\s*41,\s*55\)/i)
      expect(codeEl?.style.background).toBe('transparent')
    } finally {
      await act(async () => root.unmount())
    }
  })
})
