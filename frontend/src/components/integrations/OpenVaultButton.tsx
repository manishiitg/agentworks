import { ArrowUpRight } from 'lucide-react'
import { Button } from '../ui/Button'
import { useAuthStore } from '../../stores/useAuthStore'
import {
  gatewayBaseUrl,
  visibleProductSurfaceIDs,
} from '../../products/productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'

/** Match the current Vault management route's product and administrator checks. */
export function OpenVaultButton({
  panel = 'secrets',
}: {
  panel?: 'secrets' | 'servers'
}) {
  const allowed = useAuthStore((state) => state.user?.allowed_products)
  const admin = useAuthStore(
    (state) =>
      state.user?.is_admin === true ||
      (state.isMultiUserModeChecked && !state.isMultiUserMode),
  )
  if (
    !admin ||
    !gatewayBaseUrl() ||
    !visibleProductSurfaceIDs(allowed).includes('mcp-gateway')
  )
    return null
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => {
        try {
          sessionStorage.setItem('vault.requested-panel', panel)
        } catch {
          /* Optional destination preference. */
        }
        openProductWorkspace('mcp-gateway')
        window.dispatchEvent(
          new CustomEvent('vault-open-panel', { detail: panel }),
        )
      }}
    >
      Open Vault
      <ArrowUpRight className="h-3.5 w-3.5" />
    </Button>
  )
}
