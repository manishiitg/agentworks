import type { WorkflowNotificationState } from '../../services/workflow-notifications'

export function formatNotificationStateLabel(state: WorkflowNotificationState | 'loading'): string {
  switch (state) {
    case 'ready':
      return 'Ready'
    case 'missing_secret':
    case 'invalid_secret':
      return 'Needs attention'
    case 'loading':
      return 'Loading'
    default:
      return 'Not configured'
  }
}
