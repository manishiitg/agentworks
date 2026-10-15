// The one place that owns a workflow's layout memory: which preview device
// (mobile / tablet / laptop) it opens in, and the chat/report split width,
// which is remembered per workflow *and* per device.
//
// Invariant: opening a workflow only READS. The only writers are the user's
// own actions -- the device toggle and dragging the split. There used to be
// an openWorkflowInDefaultPreview() that reset a workflow to Tablet on every
// open, a workaround for a terminal-restore path that once wrote a stray
// "mobile"; that writer is gone, and the reset only ever threw away the
// user's choice (and orphaned the widths saved under the other devices).
// A server-revalidated workspace return may hydrate this cache with the same
// person's saved device and width; ordinary product opens remain read-only.
// Do not add a default-setting writer back here or at a call site.
export const REPORT_PREVIEW_PREFERENCE_KEY = 'workflow_report_preview_preference'
export const REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT = 'workflow-report-preview-preference-changed'
export const WORKFLOW_SPLIT_PREFERENCE_KEY = 'workflow_workspace_split_ratio'

export type ReportPreviewDevice = 'mobile' | 'tablet' | 'desktop'
export const DEFAULT_REPORT_PREVIEW_DEVICE: ReportPreviewDevice = 'tablet'

export function reportPreviewPreferenceKey(scopeId?: string | null): string {
  return scopeId ? `${REPORT_PREVIEW_PREFERENCE_KEY}:${scopeId}` : REPORT_PREVIEW_PREFERENCE_KEY
}

export function isReportPreviewDevice(value: unknown): value is ReportPreviewDevice {
  return value === 'mobile' || value === 'tablet' || value === 'desktop'
}

export function readReportPreviewPreference(scopeId?: string | null): ReportPreviewDevice {
  if (typeof window === 'undefined') return DEFAULT_REPORT_PREVIEW_DEVICE
  try {
    const saved = window.localStorage.getItem(reportPreviewPreferenceKey(scopeId))
    return isReportPreviewDevice(saved) ? saved : DEFAULT_REPORT_PREVIEW_DEVICE
  } catch {
    return DEFAULT_REPORT_PREVIEW_DEVICE
  }
}

export function writeReportPreviewPreference(
  scopeId: string | null | undefined,
  preference: ReportPreviewDevice,
): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(reportPreviewPreferenceKey(scopeId), preference)
  } catch {
    // UI preference only.
  }
  window.dispatchEvent(new CustomEvent(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, {
    detail: { preference, scopeId: scopeId ?? null },
  }))
}

function workflowSplitPreferenceKey(scopeId?: string | null, device?: ReportPreviewDevice): string {
  const scope = scopeId ? `${WORKFLOW_SPLIT_PREFERENCE_KEY}:${scopeId}` : WORKFLOW_SPLIT_PREFERENCE_KEY
  return device ? `${scope}:${device}` : scope
}

export function readWorkflowSplitPreference(scopeId?: string | null, device?: ReportPreviewDevice): number | null {
  if (typeof window === 'undefined') return null
  try {
    const value = Number.parseFloat(window.localStorage.getItem(workflowSplitPreferenceKey(scopeId, device)) || '')
    return Number.isFinite(value) && value >= 0.15 && value <= 0.85 ? value : null
  } catch {
    return null
  }
}

export function writeWorkflowSplitPreference(scopeId: string | null | undefined, ratio: number, device?: ReportPreviewDevice): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(workflowSplitPreferenceKey(scopeId, device), String(Math.max(0.15, Math.min(0.85, ratio))))
  } catch {
    // UI preference only.
  }
}
