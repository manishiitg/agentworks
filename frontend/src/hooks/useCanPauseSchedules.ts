import { useAuthStore } from '../stores/useAuthStore'

// The global schedule pause stops every schedule on the platform, so the server only accepts it from an
// administrator (PUT /api/scheduler/config). A single-user (local) run has no accounts and keeps the control.
export function useCanPauseSchedules(): boolean {
  return useAuthStore(state => state.user?.is_admin === true || (state.isMultiUserModeChecked && !state.isMultiUserMode))
}
