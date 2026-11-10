import React, { useEffect } from 'react'
import { useAppearanceStore } from '../stores/useAppearanceStore'
import { ThemeContext, type Theme } from './ThemeContext'

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const profile = useAppearanceStore(state => state.profile)
  useEffect(() => { document.documentElement.dataset.appearance = profile }, [profile])
  const theme: Theme = profile === 'clear-light' ? 'light' : 'dark'

  useEffect(() => {
    // Apply theme to document
    document.documentElement.classList.remove('light', 'dark')
    document.documentElement.classList.add(theme)
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
    
    // Save to localStorage
    localStorage.setItem('theme', theme)
  }, [theme])

  const toggleTheme = () => undefined

  const setTheme = (_newTheme: Theme) => {
    void _newTheme
  }

  return (
    <ThemeContext.Provider value={{ theme, toggleTheme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  )
}
