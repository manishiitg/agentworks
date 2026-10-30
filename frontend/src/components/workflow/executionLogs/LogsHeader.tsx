import { Braces, Filter, Terminal } from 'lucide-react'
import type { RunFolderInfo } from '../../../services/api-types'
import { formatStartedAt } from '../../../utils/duration'
import { formatDeploymentDateTime } from '../../../utils/displayTime'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../ui/select'
import { WorkspaceViewHeader } from '../WorkspaceViewHeader'
import { WorkspaceViewIconButton } from '../WorkspaceViewIconButton'
import { formatRunFolderLabel } from './helpers'

export interface LogsHeaderProps {
  startedAt?: string | null
  runFolderOptions: string[]
  runFolderInfos: RunFolderInfo[]
  selectedRunFolder: string
  setSelectedRunFolder: (folder: string) => void
  loading: boolean
  loadLogs: () => void
  onRefreshRunFolders?: () => void | Promise<void>
  showWebhookPayload?: boolean
  webhookPayloadOpen?: boolean
  onToggleWebhookPayload?: () => void
}

// Header content only; InspectorShell owns the row wrapper.
export function LogsHeader({
  startedAt,
  runFolderOptions,
  runFolderInfos,
  selectedRunFolder,
  setSelectedRunFolder,
  loading,
  loadLogs,
  onRefreshRunFolders,
  showWebhookPayload = false,
  webhookPayloadOpen = false,
  onToggleWebhookPayload,
}: LogsHeaderProps) {
  const timestampsByFolder = new Map(
    runFolderInfos.map(folder => [
      folder.name,
      folder.metadata?.started_at || folder.metadata?.created_at || null,
    ]),
  )
  const formatRunDateTime = (value?: string | null) => {
    return formatDeploymentDateTime(value)
  }
  const selectedTimestamp = formatRunDateTime(timestampsByFolder.get(selectedRunFolder))

  // Scope pickers live in the content row below the header (same as the
  // Knowledgebase source picker): the header row keeps only refresh.
  const showPickers = runFolderOptions.length > 0 || showWebhookPayload
  const pickers = (
    <div className="mt-2 flex min-w-0 flex-wrap items-center gap-2">
    {/* Run Folder Selector */}
    {runFolderOptions.length > 0 && (
    <div className="flex min-w-0 items-center gap-1.5">
    <Filter className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
    <Select
    value={selectedRunFolder}
    onValueChange={setSelectedRunFolder}
    >
    <SelectTrigger className="h-8 w-80 max-w-[48vw] bg-card px-2 text-xs font-medium shadow-none" aria-label="Execution run">
    <SelectValue placeholder="Select iteration/group">
    {selectedRunFolder && (
    <span className="flex min-w-0 items-center gap-2">
    <span className="truncate" title={selectedRunFolder}>{formatRunFolderLabel(selectedRunFolder)}</span>
    {selectedTimestamp && (
    <span className="shrink-0 text-[10px] font-normal text-muted-foreground">
    {selectedTimestamp}
    </span>
    )}
    </span>
    )}
    </SelectValue>
    </SelectTrigger>
    <SelectContent className="max-h-72">
    {runFolderOptions.map(folder => {
    const timestamp = formatRunDateTime(timestampsByFolder.get(folder))
    return (
    <SelectItem key={folder} value={folder} className="text-xs">
    <span className="flex min-w-[25rem] items-center justify-between gap-6 pr-1">
    <span title={folder}>{formatRunFolderLabel(folder)}</span>
    {timestamp && (
    <span className="text-[10px] font-normal text-muted-foreground">
    {timestamp}
    </span>
    )}
    </span>
    </SelectItem>
    )
    })}
    </SelectContent>
    </Select>
    </div>
    )}

    {showWebhookPayload && (
    <button
    type="button"
    onClick={onToggleWebhookPayload}
    aria-expanded={webhookPayloadOpen}
    className={`inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-medium transition-colors ${
    webhookPayloadOpen
    ? 'border-primary/40 bg-primary/10 text-foreground'
    : 'border-border bg-card text-muted-foreground hover:bg-muted hover:text-foreground'
    }`}
    title="View the request payload that started this webhook run"
    >
    <Braces className="h-3.5 w-3.5" />
    Payload
    </button>
    )}

    </div>
  )

  // Refresh also refreshes the run-folder list itself (onRefreshRunFolders), not just the
  // currently selected folder's logs (loadLogs). Without this, a run folder
  // that appeared after this list was last loaded (e.g. a standalone
  // execute_step run) stays invisible in the dropdown no matter how many
  // times this button is clicked.
  const headerButtons = (
    <>
    <WorkspaceViewIconButton
      label="Refresh logs and run-folder list"
      onClick={() => {
        loadLogs()
        onRefreshRunFolders?.()
      }}
      disabled={loading || !selectedRunFolder}
      spinning={loading}
    />
    </>
  )
  return (
    <div className="min-w-0 flex-1">
      <WorkspaceViewHeader
        bare
        icon={Terminal}
        title="Execution Logs"
        context={startedAt ? (
          <span className="text-xs font-normal text-muted-foreground">{formatStartedAt(startedAt)}</span>
        ) : undefined}
        actions={headerButtons}
        below={showPickers ? pickers : undefined}
      />
    </div>
  )
}
import type React from 'react'
