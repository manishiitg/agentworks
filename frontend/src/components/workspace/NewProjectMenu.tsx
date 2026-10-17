import { useEffect, useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Plus } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import { openQuickNavigation, quickNavigationItems } from '../../utils/quickNavigation'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'

/** A visible entry into the existing, permission-checked product creation flows. */
export function NewProjectMenu() {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const product = useProductSurfaceStore(state => state.productSurface)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [open, setOpen] = useState(false)
  const choices = quickNavigationItems(user, product, multiUser)
    .filter(item => item.action === 'create' && item.projectDescription)
  useEffect(() => { setOpen(false) }, [user?.id, workspace, product])
  if (!choices.length) return null
  return <Menu.Root modal={false} open={open} onOpenChange={value => {
    if (value) window.dispatchEvent(new Event('close-workspace-picker'))
    setOpen(value)
  }}>
    <Menu.Trigger asChild>
      <button type="button" className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/85 transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Plus className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        New project
      </button>
    </Menu.Trigger>
    <Menu.Portal>
      <Menu.Content aria-label="New project" side="right" align="start" sideOffset={8} collisionPadding={12}
        className="z-50 max-h-[calc(100vh-24px)] w-72 max-w-[calc(100vw-24px)] overflow-y-auto rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-xl">
        <Menu.Label className="px-2.5 pb-1.5 pt-1 text-xs font-medium text-muted-foreground">Choose a product</Menu.Label>
        {choices.map(item => item.surface && <Menu.Item key={item.id} aria-label={PRODUCT_SURFACE_LABELS[item.surface]}
          onSelect={() => {
            setOpen(false)
            if (!openQuickNavigation(item)) useChatStore.getState().addToast('This product is no longer available for you to create in.', 'info')
          }}
          className="flex cursor-pointer items-center gap-3 rounded-lg px-2.5 py-2.5 outline-none data-[highlighted]:bg-muted focus-visible:ring-2 focus-visible:ring-ring">
          <ProductSurfaceIcon surface={item.surface} className="h-5 w-5 shrink-0" />
          <span className="min-w-0">
            <span className="block text-[13px] font-medium">{PRODUCT_SURFACE_LABELS[item.surface]}</span>
            <span className="mt-0.5 block text-xs leading-4 text-muted-foreground">{item.projectDescription}</span>
          </span>
        </Menu.Item>)}
      </Menu.Content>
    </Menu.Portal>
  </Menu.Root>
}
