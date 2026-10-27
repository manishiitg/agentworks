import { readWorkflowSplitPreference } from '../../../utils/reportPreviewPreference'

export const SPARKQUILL_PREVIEW_SCOPE = 'sparkquill:parent'
const MIN_PANE = 240

/** Keep both panes usable with one responsive, draggable split. */
export function parentWorkspaceWidth(available: number, ratio: number | null): number {
  const width = Math.max(0, available)
  const min = Math.min(Math.max(MIN_PANE, width * 0.15), width / 2)
  const max = Math.max(min, Math.min(width - MIN_PANE, width * 0.85))
  const preferred = width * (1 - (ratio ?? 0.5))
  return Math.max(min, Math.min(max, preferred))
}

export function readParentSplitRatio(): number | null {
  return readWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE)
}
