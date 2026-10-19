import { useProductSettingsMenu } from '../workspace/ProductSettingsMenu'
import { ProductNavigationDescription, ProductNavigationLabel, useProductNavigationSidebar } from '../workspace/ProductTopBar'
import { Vault } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'

/** My Vault holds personal accounts for every product; shared vault access stays separate. */
export default function VaultsControl() {
  const sidebar = useProductNavigationSidebar()
  const inSettings = useProductSettingsMenu()
  const user = useAuthStore(state => state.user)
  const active = useAppStore(state => state.adminPage === 'vaults')
  const setAdminPage = useAppStore(state => state.setAdminPage)
  if (!user || user.can_create === false) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          role={inSettings ? 'menuitem' : undefined}
          data-product-navigation-action
          onClick={() => { useLLMStore.getState().setShowLLMModal(false); setAdminPage('vaults') }}
          aria-label="My vaults"
          aria-pressed={active}
          data-tour="global-vaults"
          className={`relative rounded-md p-1.5 transition-colors ${active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}
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
