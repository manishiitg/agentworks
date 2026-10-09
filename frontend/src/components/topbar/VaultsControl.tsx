import { ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { Vault } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { visibleProductSurfaceIDs } from '../../products/productSurfaceConfig'

/**
 * Navigation entry to "My vaults" (PLAT-507): any signed-in person can create vaults and share MCP connections and
 * secrets from them. The server checks ownership on every action; read-only accounts do not get the entry. An account whose product list
 * leaves out Vault (a Code-only account) does not get it either: the entry is part of the Vault product.
 */
export default function VaultsControl() {
  const sidebar = useProductNavigationSidebar()
  const user = useAuthStore(state => state.user)
  const active = useAppStore(state => state.adminPage === 'vaults')
  const setAdminPage = useAppStore(state => state.setAdminPage)
  if (!user || user.can_create === false) return null
  if (!visibleProductSurfaceIDs(user.allowed_products).includes('mcp-gateway')) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-product-navigation-action
          onClick={() => { useLLMStore.getState().setShowLLMModal(false); setAdminPage('vaults') }}
          aria-label="My vaults"
          aria-pressed={active}
          data-tour="global-vaults"
          className={`relative rounded-md p-1.5 transition-colors ${active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        >
          <Vault className="h-4 w-4 shrink-0" />
          <ProductNavigationLabel>My vaults</ProductNavigationLabel>
        </button>
      </TooltipTrigger>
      <TooltipContent side={sidebar ? 'right' : 'bottom'}>My vaults</TooltipContent>
    </Tooltip>
  )
}
