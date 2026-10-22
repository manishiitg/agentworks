import { createContext, useContext, type ReactNode } from 'react'

// Keep context identities outside component modules so live UI edits do not
// leave the toolbar and its portaled controls using different providers.
export const ProductNavigationContext = createContext(false)
export const CompactProductNavigationContext = createContext(false)
export const SettingsMenuContext = createContext<false | 'menu' | 'tiles'>(false)
/** Product actions belong to the header picker, separate from account settings. */
export const ProductPickerActionsContext = createContext<ReactNode>(null)

export const useProductNavigationSidebar = () => useContext(ProductNavigationContext)
export const useCompactProductNavigation = () => useContext(CompactProductNavigationContext)
export const useProductSettingsMenu = () => Boolean(useContext(SettingsMenuContext))
export const useProductSetupTiles = () => useContext(SettingsMenuContext) === 'tiles'
