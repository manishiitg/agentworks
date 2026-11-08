import React from 'react'
import { REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, isReportPreviewDevice, readReportPreviewPreference, type ReportPreviewDevice } from '../../../utils/reportPreviewPreference'

export const WORKFLOW_REPORT_EXPORT_EVENT = 'workflow-report-export-requested'
type PreviewDevice = ReportPreviewDevice

// Keep the preview width in sync with the workflow's saved device preference.
export function usePreviewDevice(scopeId?: string | null): PreviewDevice {
  const [pref, setPref] = React.useState<PreviewDevice>(() => readReportPreviewPreference(scopeId))
  React.useEffect(() => {
    // Re-read when the workflow (scope) changes.
    setPref(readReportPreviewPreference(scopeId))
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail
      if ((detail?.scopeId ?? null) !== (scopeId ?? null)) return
      const p = detail?.preference
      if (isReportPreviewDevice(p)) setPref(p)
    }
    window.addEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, handler)
    return () => window.removeEventListener(REPORT_PREVIEW_PREFERENCE_CHANGED_EVENT, handler)
  }, [scopeId])
  return pref
}
