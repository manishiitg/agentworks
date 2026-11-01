import { WORK_PANELS, type ProductPanel } from '../productPanels'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

/** Preserve each product/mode's existing toolbar inventory for all navigation surfaces. */
export function workWorkspacePanels({
  profileId = 'work',
  cowork = false,
  readOnly = false,
  showShell = false,
  enabledPanels,
}: {
  profileId?: 'work'
  cowork?: boolean
  readOnly?: boolean
  showShell?: boolean
  enabledPanels?: Set<string>
} = {}) {
  const pick = (ids: string[]) =>
    ids
      .map((id) => WORK_PANELS.views.find((item) => item.id === id))
      .filter((item): item is ProductPanel<WorkWorkspaceView> => !!item)
  const views = cowork
    ? [
        ...pick(['dashboard', 'browser', 'schedules', 'memory', 'plan']),
        ...WORK_PANELS.ops.filter((item) => item.id === 'costs'),
      ]
    : WORK_PANELS.views
  const ops = cowork
    ? WORK_PANELS.ops.filter((item) => item.id === 'files' || item.id === 'history')
    : WORK_PANELS.ops
  return {
    views: views
      .filter((item) =>
        readOnly
          ? item.id === 'dashboard' && (!enabledPanels || enabledPanels.has('dashboard'))
          : !enabledPanels || enabledPanels.has(item.id) || item.id === 'plan' || item.id === 'suggestions' || (item.id === 'memory' && enabledPanels.has('skills'))
      )
      .filter((item) => isWorkWorkspaceViewEnabled(item.id, undefined)),
    ops: ops
      .filter((item) => !readOnly && isWorkWorkspaceViewEnabled(item.id, enabledPanels))
      .filter((item) => item.id !== 'shell' || (showShell && !cowork)),
    setup: readOnly
      ? []
      : WORK_PANELS.setup.filter((item) =>
          isWorkWorkspaceViewEnabled(item.id, enabledPanels)
        ),
  }
}
