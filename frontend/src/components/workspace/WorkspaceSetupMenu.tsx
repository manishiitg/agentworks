import type { ReactNode } from 'react'
import { ProductSettingsMenu } from './ProductSettingsMenu'
import ProvidersControl from '../topbar/ProvidersControl'
import McpControl from '../topbar/McpControl'
import VaultsControl from '../topbar/VaultsControl'
import UsersControl from '../topbar/UsersControl'

/** Every surface uses the same Setup actions and their existing permission gates. */
export function WorkspaceSetupMenu({ activity }: { activity?: ReactNode }) {
  return <ProductSettingsMenu activity={activity}>
    <ProvidersControl />
    <McpControl />
    <VaultsControl />
    <UsersControl />
  </ProductSettingsMenu>
}
