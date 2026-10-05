// Preserve the full schedule identity, including timing, across initial
// discovery, schedule history, Ctrl+K and Global Monitor. The shared tab
// component handles visual overflow and exposes the full title on hover.
export function scheduleTabLabel(jobName?: string | null): string {
  return jobName?.trim() || 'Schedule'
}
