import { readWorkflowSplitPreference, type ReportPreviewDevice } from '../../../utils/reportPreviewPreference'

export const SPARKQUILL_PREVIEW_SCOPE = 'sparkquill:parent'
const MIN_PANE = 240

/** Use the platform's device presets, keeping enough room to use both panes. */
export function parentWorkspaceWidth(available: number, device: ReportPreviewDevice, ratio: number | null): number {
  const width = Math.max(0, available)
  const min = Math.min(Math.max(MIN_PANE, width * 0.15), width / 2)
  const max = Math.max(min, Math.min(width - MIN_PANE, width * 0.85))
  const preferred = ratio === null
    ? device === 'mobile' ? 494 : device === 'desktop' ? width - 480 : width / 2
    : width * (1 - ratio)
  return Math.max(min, Math.min(max, preferred))
}

export function readParentSplitRatio(device: ReportPreviewDevice): number | null {
  return readWorkflowSplitPreference(SPARKQUILL_PREVIEW_SCOPE, device)
}
