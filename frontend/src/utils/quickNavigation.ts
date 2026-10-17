import type { AuthUser } from '../services/api'
import { useCommandDialogStore, type ProductCreateSurface } from '../stores/useCommandDialogStore'
import { hasProductCreateAccess } from './workflowPermissions'
import { gatewayAdminUrl, PRODUCT_SURFACE_LABELS, navigationProductSurfaceIDs, visibleProductSurfaceIDs, type ProductSurface } from '../products/productSurfaceConfig'
import { useAppStore } from '../stores/useAppStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { openProductWorkspace } from './productWorkspaceNavigation'

export type QuickNavigationScope = 'active' | 'workflows' | 'relays' | 'crew' | 'code' | 'chats' | 'products' | 'panels' | 'create'
export type NavigationAction = 'active' | 'browse' | 'activity' | 'schedules' | 'providers' | 'users' | 'mcp' | 'vault-audit' | 'vault-connect' | 'create'
export type QuickNavigationItem = {
  type: 'product' | 'menu'
  id: string
  label: string
  subtitle: string
  searchText?: string
  isActive: boolean
  lastAccessedAt: number
  activeSession?: undefined
  hasLocalActivity: false
  surface?: ProductSurface
  action?: NavigationAction
  scope?: QuickNavigationScope
  /** Present for primary project creators shown in the sidebar's New project menu. */
  projectDescription?: string
}

const CREATION_LABELS: Record<ProductCreateSurface, string> = {
  agentworks: 'Create new workflow', relays: 'Create new Flow', work: 'Create new Crew',
  code: 'Create new Crew', 'video-studio': 'Create new video project',
}

// Keep this beside the shared creation commands so a product consolidation has
// one inventory to update; the sidebar does not maintain separate destinations.
const PROJECT_CREATION_DESCRIPTIONS: Partial<Record<ProductCreateSurface, string>> = {
  agentworks: 'Work toward a measurable goal.',
  work: 'A specialist for repeat work.',
  code: 'Work with an AI agent on a project.',
  relays: 'Run repeatable steps automatically.',
}

const GLOBAL_PAGE_SURFACES: ProductSurface[] = ['agentworks', 'relays', 'work', 'code', 'mcp-gateway', 'bullpen']

/** The same deployment/account product list and role gates as the sidebar. */
export function quickNavigationItems(
  user: AuthUser | null,
  current: ProductSurface,
  isMultiUserMode = useAuthStore.getState().isMultiUserMode,
): QuickNavigationItem[] {
  const products = visibleProductSurfaceIDs(user?.allowed_products)
  const common = { lastAccessedAt: 0, hasLocalActivity: false as const }
  const items: QuickNavigationItem[] = navigationProductSurfaceIDs(user?.allowed_products).map(surface => ({
    ...common, type: 'product', id: `product:${surface}`, label: PRODUCT_SURFACE_LABELS[surface],
    ...((surface === 'work' || surface === 'code') ? { searchText: 'Code Crew workspace' } : {}),
    subtitle: surface === 'knowledgebase' ? 'Product · shared knowledge for your agents' : 'Product · open workspace', isActive: surface === current || (surface === 'code' || surface === 'work') && (current === 'code' || current === 'work'), surface,
  }))
  const menu = (action: NavigationAction, label: string, description: string) => {
    items.push({ ...common, type: 'menu', id: `menu:${action}`, label, subtitle: `Menu · ${description}`, isActive: false, action })
  }
  const browse = (scope: QuickNavigationScope, label: string) => {
    items.push({ ...common, type: 'menu', id: `browse:${scope}`, label, subtitle: 'Browse · show the full list', isActive: false, action: scope === 'active' ? 'active' : 'browse', scope })
  }
  browse('active', 'Running work')
  if (products.includes('agentworks')) { browse('workflows', 'All workflows'); browse('chats', 'All chats') }
  if (products.includes('relays')) browse('relays', 'All Flows')
  if (products.includes('work') || products.includes('code')) browse('crew', 'All Crews')
  if (products.length) browse('products', 'All products')
  if (products.includes(current)) browse('panels', 'All panels and tabs')
  const creatableOrigins = products.filter((surface): surface is ProductCreateSurface => surface in CREATION_LABELS)
    .filter(surface => hasProductCreateAccess(user, isMultiUserMode, surface))
  const creationSurfaces = creatableOrigins.includes('code') ? creatableOrigins.filter(surface => surface !== 'work') : creatableOrigins
  if (creationSurfaces.length) browse('create', 'Create new…')
  for (const surface of creationSurfaces) {
    items.push({ ...common, type: 'menu', id: `create:${surface}`, label: CREATION_LABELS[surface],
      subtitle: `Create · ${PRODUCT_SURFACE_LABELS[surface]} · open the creation form`, isActive: false, action: 'create', surface,
      projectDescription: PROJECT_CREATION_DESCRIPTIONS[surface] })
  }
  if (products.includes('agentworks')) menu('activity', 'Activity', 'Automation activity and recent runs')
  if (products.some(surface => surface === 'agentworks' || surface === 'work')) {
    menu('schedules', 'Schedules and triggers', 'Scheduled work and automation triggers')
  }
  if (products.some(surface => GLOBAL_PAGE_SURFACES.includes(surface))) {
    menu('providers', 'AI models & accounts', 'AI accounts, models and costs')
    if (user?.is_admin) menu('users', 'Users and access', 'Manage users and permissions')
    if (user) menu('mcp', 'Connect an AI agent (MCP)', 'Connect an agent to this server')
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
  if (item.action === 'create') {
    // Resolve the admitted destination again instead of trusting a stale row.
    const destination = quickNavigationItems(user, current).find(candidate => candidate.id === item.id)?.surface
    if (!destination || !(destination in CREATION_LABELS)) return false
    openProductWorkspace(destination)
    useCommandDialogStore.getState().requestProductCreate(destination as ProductCreateSurface)
    return true
  }
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
  else if (item.action === 'activity') { app.setActivityWorkflowPath(null); app.setShowWorkflowsOverview(true) }
  else if (item.action === 'schedules') app.setShowSchedulesOverview(true)
  return true
}
