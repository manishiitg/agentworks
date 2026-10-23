import { useProductSettingsMenu } from '../workspace/ProductSettingsMenu'
import { ProductNavigationDescription, ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { Vault } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { openLastProjectVaultChat } from '../../utils/projectVaultNavigation'
import { useChatStore } from '../../stores/useChatStore'

/** My Vault holds personal accounts for every product; shared vault access stays separate. */
export default function VaultsControl() {
  const sidebar = useProductNavigationSidebar()
  const inSettings = useProductSettingsMenu()
  const user = useAuthStore(state => state.user)
  if (!user || user.can_create === false) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          role={inSettings ? 'menuitem' : undefined}
          data-product-navigation-action
          onClick={() => { void openLastProjectVaultChat().catch(error => useChatStore.getState().addToast(error.message, 'error')) }}
          aria-label="My vaults"
          data-tour="global-vaults"
          className="relative rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <Vault className="h-4 w-4 shrink-0" />
          <ProductNavigationLabel>My vaults</ProductNavigationLabel>
          <ProductNavigationDescription>Secrets &amp; connections</ProductNavigationDescription>
        </button>
      </TooltipTrigger>
      <TooltipContent side={sidebar ? 'right' : 'bottom'}>My vaults</TooltipContent>
    </Tooltip>
  )
}
