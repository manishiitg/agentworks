import { visibleProductSurfaceIDs } from '../productSurfaceConfig'

/** Navigation only: the server remains responsible for authorizing Vault data. */
export function canNavigateToSharedVault(user: { is_admin?: boolean; is_vault_reader?: boolean; allowed_products?: string[] | null } | null | undefined): boolean {
  return Boolean(user && (user.is_admin === true || user.is_vault_reader === true) && visibleProductSurfaceIDs(user.allowed_products).includes('mcp-gateway'))
}
