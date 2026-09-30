import { useAuthStore } from '../stores/useAuthStore'

// Wait for auth mode discovery before exposing server-wide review screens.
// The API checks the current directory record on every request as well.
export function useCanReviewCode(): boolean {
  return useAuthStore(state => state.user?.is_admin === true || state.user?.is_code_reviewer === true ||
    (state.isMultiUserModeChecked && !state.isMultiUserMode))
}
