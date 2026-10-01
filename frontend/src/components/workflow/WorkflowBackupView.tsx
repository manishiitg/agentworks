import React, { useCallback } from 'react'
import { agentApi } from '../../services/api'
import type { WorkflowBackupInfoResponse, WorkflowBackupStrategyInfo } from '../../services/api-types'
import BackupPopupBody from '../backup-publish/BackupPopupBody'

interface WorkflowBackupViewProps {
  workspacePath: string | null
  // Called whenever backup info is (re)loaded, so the parent can keep an
  // at-a-glance status indicator (e.g. the toolbar dot) in sync.
  onStateLoaded?: (state: string) => void
  headerAction?: React.ReactNode
}

const FALLBACK_SUPPORTED_STRATEGIES: WorkflowBackupStrategyInfo[] = [
  {
    id: 'git',
    label: 'GitHub / remote Git (recommended)',
    description: 'Protects config, planning, knowledge, and learnings off this device.',
    best_for: ['workflow', 'planning', 'knowledgebase', 'learnings']
  },
  {
    id: 'object_store',
    label: 'R2 / S3 / B2',
    description: 'For run folders, media, and large files that should not live in git.',
    best_for: ['runs', 'large-artifacts', 'media']
  },
  {
    id: 'huggingface',
    label: 'HuggingFace Hub',
    description: 'For dataset and model-style backups with revisions.',
    best_for: ['datasets', 'models', 'media']
  }
]

const getBackupSummary = (backupInfo: WorkflowBackupInfoResponse | null): string => {
  const state = backupInfo?.effective_state
  if (!backupInfo?.config?.enabled) {
    return 'No backup yet. If this laptop is lost, this workflow is lost. Set one up with /backup in chat.'
  }
  if (state === 'local_only') {
    return 'This backup lives only on this laptop. Add a remote destination with /backup in chat.'
  }
  switch (state) {
    case 'configured_not_verified':
      return 'Backup is set up. Waiting for the first successful run.'
    case 'running':
      return 'A backup is running now.'
    case 'healthy':
      return 'Backed up. Everything is protected.'
    case 'partial':
      return 'Partly backed up. Check the destinations below.'
    case 'failed':
      return 'Last backup failed.'
    case 'skipped':
      return 'Last backup was skipped.'
    default:
      return 'Waiting for backup status.'
  }
}

const WorkflowBackupView: React.FC<WorkflowBackupViewProps> = ({
  workspacePath,
  onStateLoaded,
  headerAction,
}) => {
  const loadInfo = useCallback(async () => {
    if (!workspacePath) throw new Error('No workflow is selected')
    return agentApi.getWorkflowBackup(workspacePath)
  }, [workspacePath])

  return (
    <BackupPopupBody
      loadInfo={loadInfo}
      onStateLoaded={onStateLoaded}
      fallbackStrategies={FALLBACK_SUPPORTED_STRATEGIES}
      subtitle="Remote backups"
      emptyDestinationsText="No backup destinations yet — set one up with /backup in chat."
      destinationsHelp="Status updates automatically after each run."
      askContext={{
        workspacePath,
        strategyVerb: 'back up this workflow with',
      }}
      getSummary={getBackupSummary}
      headerAction={headerAction}
    />
  )
}

export default WorkflowBackupView
