import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { agentApi } from '../../services/api'
import { enabledProductSurfaces, intersectAllowedProductSurfaces } from '../../products/productSurfaceConfig'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { connectedWorkHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useAppPath } from '../../utils/appNavigation'
import { parseProductRoute } from '../../utils/productRoutes'

type Crew = Awaited<ReturnType<typeof agentApi.listWorkspaceNavigation>>['workspaces'][number]
type Project = Pick<Crew, 'id' | 'name' | 'icon' | 'mode' | 'owned_by_caller' | 'owner_name' | 'shared_line' | 'location' | 'mac' | 'state' | 'where'> & { kind: 'crew' }
const Inventory = createContext<{ projects: Project[]; canBrowse: boolean; loading: boolean; error: boolean; retry: () => void }>({
  projects: [], canBrowse: false, loading: false, error: false, retry: () => {},
})
export const useWorkspaceProjects = () => useContext(Inventory)

/** One authorized inventory read for the sidebar and the full directory. */
export function WorkspaceProjectsProvider({ children }: { children: ReactNode }) {
  const user = useAuthStore(state => state.user)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = `${workspace}:${user?.id || ''}`
  const crewId = useProductSurfaceStore(state => state.selectedWorkProjectId)
  const product = useProductSurfaceStore(state => state.productSurface)
  const path = useAppPath()
  const allowed = intersectAllowedProductSurfaces(enabledProductSurfaces(), user?.allowed_products)
  const canBrowse = allowed.includes('work')
  const [directory, setDirectory] = useState<{ scope: string; crews: Crew[]; error: boolean } | null>(null)
  const [refresh, setRefresh] = useState(0)
  useLiveRefetch(() => setRefresh(value => value + 1), {
    kinds: ['sessions'], fallbackMs: 60000, safetyMs: 60000, minIntervalMs: 5000,
    enabled: canBrowse,
  })
  useEffect(() => {
    let disposed = false
    const identity = captureChatIdentity()
    if (!canBrowse) {
      setDirectory({ scope, crews: [], error: false })
      return
    }
    void agentApi.listWorkspaceNavigation().then(result => {
      if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, crews: result.workspaces, error: false })
    }).catch(() => {
      if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, crews: [], error: true })
    })
    return () => { disposed = true }
  }, [scope, refresh, canBrowse, crewId])
  const current = directory?.scope === scope ? directory : null
  const projects = [
    ...(canBrowse ? (current?.crews || []).filter(crew => crew.profile_id === 'work').map(crew => ({ id: crew.id, name: crew.name, icon: crew.icon, mode: crew.mode, owned_by_caller: crew.owned_by_caller, owner_name: crew.owner_name, shared_line: crew.shared_line, location: crew.location, mac: crew.mac, state: crew.state, where: crew.where, kind: 'crew' as const })) : []),
  ].sort((a, b) => a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind) || a.id.localeCompare(b.id))

  const route = parseProductRoute(path)
  const admittedProject = canBrowse && route?.surface === 'work' && product === 'work' && route.id === crewId
    ? current?.crews.find(crew => crew.id === crewId && !(crew.owned_by_caller === false && crew.mode === 'run'))?.id
    : undefined
  // The shared shell records an admitted visit even with the phone drawer closed.
  useEffect(() => {
    if (admittedProject) useProjectMenuHistoryStore.getState().visit(connectedWorkHistoryScope(), connectedProjectKey(getRawActiveWorkspaceId(), 'crew', admittedProject))
  }, [admittedProject, scope])

  return <Inventory.Provider value={{ projects, canBrowse, loading: !current, error: Boolean(current?.error), retry: () => setRefresh(value => value + 1) }}>{children}</Inventory.Provider>
}
