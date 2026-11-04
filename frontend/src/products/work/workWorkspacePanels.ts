import { WORK_PANELS } from '../productPanels'
import { isWorkWorkspaceViewEnabled } from './workViewGating'

/** Preserve the product's existing toolbar inventory for all navigation surfaces. */
export function workWorkspacePanels({
  profileId = 'work',
  readOnly = false,
  showShell = false,
  enabledPanels,
}: {
  profileId?: 'work'
  readOnly?: boolean
  showShell?: boolean
  enabledPanels?: Set<string>
} = {}) {
  return {
    views: WORK_PANELS.views
      .filter((item) =>
        readOnly
          ? item.id === 'dashboard' && (!enabledPanels || enabledPanels.has('dashboard'))
          : !enabledPanels || enabledPanels.has(item.id) || item.id === 'goals' || item.id === 'plan' || item.id === 'suggestions' || (item.id === 'memory' && enabledPanels.has('skills'))
      )
      .filter((item) => isWorkWorkspaceViewEnabled(item.id, undefined)),
    ops: WORK_PANELS.ops
      .filter((item) => !readOnly && isWorkWorkspaceViewEnabled(item.id, enabledPanels))
      .filter((item) => item.id !== 'shell' || showShell),
    setup: readOnly
      ? []
      : WORK_PANELS.setup.filter((item) =>
          isWorkWorkspaceViewEnabled(item.id, enabledPanels)
        ),
  }
}
