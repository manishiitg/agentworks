import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import '../../src/App.css'
import { WorkspaceAccountMenu } from '../../src/components/workspace/WorkspaceAccountMenu'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import { useAuthStore } from '../../src/stores/useAuthStore'
import { useProductSurfaceStore } from '../../src/stores/useProductSurfaceStore'
import { useProductNavigationToolbarStore } from '../../src/stores/useProductNavigationToolbarStore'

// Actual account menu with synthetic identity; no chat, account connection or turn.
useAuthStore.setState({ user: { id: 'example-person', username: 'Example account', email: 'person@example.test', can_create: true, is_admin: false, allowed_products: null } as never, isMultiUserMode: true, providers: [{ type: 'oauth' }] as never })
useProductSurfaceStore.setState({ productSurface: 'work' })
const slot = document.createElement('div')
slot.className = 'fixed bottom-3 left-2'
document.body.append(slot)
useProductNavigationToolbarStore.setState({ compactSettingsTarget: slot, sidebarSettingsTarget: null })
createRoot(document.getElementById('root')!).render(<TooltipProvider><div data-account-products-fixture><WorkspaceAccountMenu /></div></TooltipProvider>)
