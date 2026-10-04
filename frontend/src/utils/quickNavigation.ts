import { gatewayAdminUrl, PRODUCT_SURFACE_LABELS, visibleProductSurfaceIDs, type ProductSurface } from '../products/productSurfaceConfig'
import { useAppStore } from '../stores/useAppStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { openProductWorkspace } from './productWorkspaceNavigation'

export type QuickNavigationScope = 'active' | 'workflows' | 'relays' | 'crew' | 'code' | 'chats' | 'products'
export type NavigationAction = 'active' | 'browse' | 'activity' | 'schedules' | 'providers' | 'users' | 'mcp' | 'vault-audit' | 'vault-connect'
export type QuickNavigationItem = {
  type: 'product' | 'menu'
  id: string
  label: string
  subtitle: string
  isActive: boolean
  lastAccessedAt: number
  activeSession?: undefined
  hasLocalActivity: false
  surface?: ProductSurface
  action?: NavigationAction
  scope?: QuickNavigationScope
}

const GLOBAL_PAGE_SURFACES: ProductSurface[] = ['agentworks', 'relays', 'work', 'code', 'mcp-gateway']

/** The same deployment/account product list and role gates as the sidebar. */
export function quickNavigationItems(
  user: { allowed_products?: string[] | null; is_admin?: boolean; is_code_reviewer?: boolean } | null,
  current: ProductSurface,
): QuickNavigationItem[] {
  const products = visibleProductSurfaceIDs(user?.allowed_products)
  const common = { lastAccessedAt: 0, hasLocalActivity: false as const }
  const items: QuickNavigationItem[] = products.map(surface => ({
    ...common, type: 'product', id: `product:${surface}`, label: PRODUCT_SURFACE_LABELS[surface],
    subtitle: 'Product · open workspace', isActive: surface === current, surface,
  }))
  const menu = (action: NavigationAction, label: string, description: string) => {
    items.push({ ...common, type: 'menu', id: `menu:${action}`, label, subtitle: `Menu · ${description}`, isActive: false, action })
  }
  const browse = (scope: QuickNavigationScope, label: string) => {
    items.push({ ...common, type: 'menu', id: `browse:${scope}`, label, subtitle: 'Browse · show the full list', isActive: false, action: scope === 'active' ? 'active' : 'browse', scope })
  }
  browse('active', 'Running work')
  if (products.includes('agentworks')) { browse('workflows', 'All workflows'); browse('chats', 'All chats') }
  if (products.includes('relays')) browse('relays', 'All Relays')
  if (products.includes('work')) browse('crew', 'All Crews')
  if (products.includes('code')) browse('code', 'All Code projects')
  if (products.length) browse('products', 'All products')
  if (products.includes('agentworks')) menu('activity', 'Activity', 'Automation activity and recent runs')
  if (products.some(surface => surface === 'agentworks' || surface === 'work')) {
    menu('schedules', 'Schedules and triggers', 'Scheduled work and automation triggers')
  }
  if (products.some(surface => GLOBAL_PAGE_SURFACES.includes(surface))) {
    menu('providers', 'Providers', 'AI accounts, models and costs')
    if (user?.is_admin) menu('users', 'Users and access', 'Manage users and permissions')
    if (user?.is_admin || user?.is_code_reviewer) menu('mcp', 'Connect an AI agent (MCP)', 'Connect an agent to this server')
  }
  if (current === 'mcp-gateway' && products.includes(current) && gatewayAdminUrl() && user?.is_admin) {
    menu('vault-audit', 'Vault audit logs', 'Vault · audit and analysis')
    menu('vault-connect', 'Vault MCP endpoint', 'Vault · connection details')
  }
  return items
}

/** Open a menu on an allowed surface that actually renders its page. */
export function openQuickNavigation(item: QuickNavigationItem): boolean {
  const user = useAuthStore.getState().user
  const current = useProductSurfaceStore.getState().productSurface
  // Re-check at activation time in case permissions changed while open.
  if (!quickNavigationItems(user, current).some(candidate => candidate.id === item.id)) return false
  if (item.type === 'product' && item.surface) {
    openProductWorkspace(item.surface)
    return true
  }
  if (!item.action || item.scope) return false
  if (item.action === 'vault-audit' || item.action === 'vault-connect') {
    const panel = item.action === 'vault-audit' ? 'audit' : 'connect'
    openProductWorkspace('mcp-gateway')
    window.dispatchEvent(new CustomEvent('vault-open-panel', { detail: panel }))
    return true
  }
  const products = visibleProductSurfaceIDs(user?.allowed_products)
  const destinations = item.action === 'activity' ? ['agentworks']
    : item.action === 'schedules' ? ['agentworks', 'work'] : GLOBAL_PAGE_SURFACES
  const destination = destinations.includes(current) && products.includes(current) ? current
    : products.find(surface => destinations.includes(surface))
  if (!destination) return false
  openProductWorkspace(destination)
  const app = useAppStore.getState()
  if (item.action === 'providers') useLLMStore.getState().setShowLLMModal(true)
  else if (item.action === 'users' || item.action === 'mcp') app.setAdminPage(item.action)
  else if (item.action === 'activity') app.setShowWorkflowsOverview(true)
  else if (item.action === 'schedules') app.setShowSchedulesOverview(true)
  return true
}
