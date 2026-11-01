import { ChromeExtensionConnection, useChromeExtensionConnection } from './ChromeExtensionConnection'
import { Button } from '../ui/Button'
import { Monitor, Loader2 } from 'lucide-react'
import BrowserAutomationSettings, { type BrowserAutomationMode } from '../BrowserAutomationSettings'
import WorkflowLiveBrowser, { type BrowserEmptyStateActions } from './WorkflowLiveBrowser'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'

interface BrowserWorkspacePanelProps {
  workspacePath: string | null
  browserMode: BrowserAutomationMode
  onBrowserModeChange: (mode: BrowserAutomationMode) => void
  cdpPort?: number
  onCdpPortChange?: (port: number) => void
  cdpConnected: boolean | null
  cdpError: string | null
  cdpChecking: boolean
  onCheckCdpConnection: (port: number) => void
  readOnly?: boolean
  dirty?: boolean
  saving?: boolean
  onSave?: () => void
  scopeNoun?: 'workflow' | 'project'
  profileId?: string
}

/** One inline project choice; account pairing remains reusable across projects. */
export function BrowserWorkspacePanel({
  workspacePath, browserMode, onBrowserModeChange, cdpPort, onCdpPortChange,
  cdpConnected, cdpError, cdpChecking, onCheckCdpConnection, readOnly = false,
  dirty = false, saving = false, onSave, scopeNoun = 'workflow', profileId,
}: BrowserWorkspacePanelProps) {
  const extensionProfile = profileId || (workspacePath?.startsWith('Workflow/') ? 'workflow' : undefined)
  const extensionAvailable = ['work', 'workflow'].includes(extensionProfile || '') && !!workspacePath && !readOnly
  const connection = useChromeExtensionConnection(workspacePath, extensionProfile, !extensionAvailable)
  const displayMode = connection.status.mode || browserMode
  const directMode = connection.status.allowed_modes?.find(mode => mode !== 'extension')
  const directLabel = directMode === 'cdp' ? 'Local Chrome · Direct' : 'In-app browser'
  const useWorkspaceBrowser = async () => {
    if (!directMode) return
    if (!await connection.selectWorkspace(directMode, cdpPort)) return false
    onBrowserModeChange(directMode)
    return true
  }
  const controls = !readOnly && workspacePath ? <div className="space-y-3">
    {extensionAvailable && <ChromeExtensionConnection key={`${extensionProfile}:${workspacePath}`} connection={connection} workspaceBrowser={directMode ? { label: directLabel, secondary: directMode === 'cdp', selected: !connection.status.selected && displayMode === directMode, onSelect: useWorkspaceBrowser } : undefined} />}
    {directMode === 'cdp' && displayMode === 'cdp' && !connection.status.selected && <details className="mt-2 text-xs">
      <summary className="cursor-pointer text-muted-foreground">Connection details</summary>
      <div className="pt-3"><BrowserAutomationSettings inline browserMode={displayMode} onBrowserModeChange={onBrowserModeChange} cdpPort={cdpPort} onCdpPortChange={onCdpPortChange} cdpConnected={cdpConnected} cdpError={cdpError} cdpChecking={cdpChecking} onCheckCdpConnection={onCheckCdpConnection} readOnly={readOnly || connection.busy} scopeNoun={scopeNoun} />{onSave && (dirty || saving) && <Button size="sm" disabled={saving} onClick={onSave}>{saving ? 'Saving…' : 'Apply connection settings'}</Button>}</div>
    </details>}
  </div> : null
  const hasSavedBrowser = connection.status.browsers?.some(browser => browser.enabled)
  const emptyBrowser = !readOnly && workspacePath ? ({ startBrowser, startingBrowser, canStart }: BrowserEmptyStateActions) => <div className="space-y-4 text-center" aria-label="Open a browser">
    <p className="text-sm text-muted-foreground">{displayMode === 'headless' || displayMode === 'cdp' ? 'Ask your agent to open a website.' : hasSavedBrowser ? 'Choose a browser above.' : 'Add a browser above to start.'}</p>
    {canStart && (displayMode === 'headless' || displayMode === 'cdp') && <Button type="button" disabled={startingBrowser || connection.busy} onClick={() => { void startBrowser() }} className="gap-2">
      {startingBrowser ? <Loader2 className="h-4 w-4 animate-spin" /> : <Monitor className="h-4 w-4" />}
      {startingBrowser ? 'Opening browser…' : 'Open browser'}
    </Button>}
  </div> : undefined
  const extensionView = !readOnly && (connection.status.selected || displayMode === 'extension' || displayMode === 'none')

  return <div className="relative flex h-full min-h-0 flex-1 flex-col">
    {connection.loading ? <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Checking browser connection…</div> : extensionView ? <>
      <WorkspaceViewHeader icon={Monitor} title="Browser" />
      <div className="min-h-0 flex-1 overflow-y-auto p-4" aria-label="Browser content">{controls}</div>
    </> : <WorkflowLiveBrowser workspacePath={workspacePath} scopeNoun={scopeNoun} profileId={profileId} emptyContent={emptyBrowser} browserSelection={controls && <div className="max-h-[40vh] shrink-0 overflow-y-auto border-b border-border px-3 py-2">{controls}</div>} />}
  </div>
}
