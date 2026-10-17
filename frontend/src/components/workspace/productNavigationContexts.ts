import { createContext, useContext } from 'react'

// Keep context identities outside component modules so live UI edits do not
// leave the toolbar and its portaled controls using different providers.
export const ProductNavigationContext = createContext(false)
export const CompactProductNavigationContext = createContext(false)
export const SettingsMenuContext = createContext<false | 'menu' | 'tiles'>(false)

export const useProductNavigationSidebar = () => useContext(ProductNavigationContext)
export const useCompactProductNavigation = () => useContext(CompactProductNavigationContext)
export const useProductSettingsMenu = () => Boolean(useContext(SettingsMenuContext))
export const useProductSetupTiles = () => useContext(SettingsMenuContext) === 'tiles'
