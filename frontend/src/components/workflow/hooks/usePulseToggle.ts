import { useCallback, useState } from 'react'

export function usePulseToggle(
  workspacePath: string | null,
  monitorOn: boolean,
  update: (path: string, changes: { pulse_enabled: boolean }) => Promise<unknown>,
  notify: (message: string, type: 'success' | 'error') => void,
) {
  const [monitorSaving, setMonitorSaving] = useState(false)
  const toggleMonitor = useCallback(() => {
    if (!workspacePath || monitorSaving) return
    setMonitorSaving(true)
    // Goal setup is available separately; missing observations must not block
    // the user's explicit request to enable reviews.
    void update(workspacePath, { pulse_enabled: !monitorOn })
      .then(() => notify(monitorOn ? 'Autopilot turned off' : 'Autopilot turned on', 'success'))
      .catch(error => notify(error instanceof Error ? error.message : 'Could not update Autopilot', 'error'))
      .finally(() => setMonitorSaving(false))
  }, [workspacePath, monitorOn, monitorSaving, update, notify])
  return { monitorSaving, toggleMonitor }
}
