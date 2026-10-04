import React from 'react'
import type { ChatTab } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { isWorkflowReadOnly } from '../../utils/workflowPermissions'
import { ChatTabPill } from './ChatTabPill'
import type { ProductSurface } from '../../products/productSurfaceConfig'

export interface AgentWorksChatTabItemProps {
  tab: ChatTab
  isActive: boolean
  canClose: boolean
  isBlank: boolean
  displayName?: string
  titleOverride?: string
  productSurface?: ProductSurface
  onTabClick: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onRenameTab?: (tab: ChatTab, name: string) => Promise<boolean | void>
  onMakeInteractive?: (tabId: string) => void
}

/** Runtime adapter for the shared Chat tab presentation. */
export const AgentWorksChatTabItem = React.memo<AgentWorksChatTabItemProps>(({ onRenameTab, tab, ...props }) => {
  const readOnly = useAuthStore(state => isWorkflowReadOnly(state.user, state.isMultiUserMode))
  return <ChatTabPill {...props} tab={tab} readOnly={readOnly} onRename={onRenameTab ? name => onRenameTab(tab, name) : undefined} />
})
AgentWorksChatTabItem.displayName = 'AgentWorksChatTabItem'
