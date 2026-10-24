import { cloneElement, isValidElement, type ReactNode } from 'react'
import { ChromeExtensionConnection, useChromeExtensionConnection } from './ChromeExtensionConnection'
import { Button } from '../ui/Button'
import { Monitor, PlugZap, Loader2 } from 'lucide-react'
import BrowserAutomationSettings, { type BrowserAutomationMode } from '../BrowserAutomationSettings'
import { isBrowserCDPEnabled } from '../../utils/runtimeCapabilities'
import WorkflowLiveBrowser, { type BrowserEmptyStateActions } from './WorkflowLiveBrowser'
import { WorkspaceViewActions, type WorkspaceViewActionsProps } from './WorkspaceViewActions'
import { useWorkspacePanelAssistant } from '../workspace/WorkspacePanelNavigation'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'

interface BrowserWorkspacePanelProps {
  workspacePath: string | null
  browserMode: BrowserAutomationMode
  onBrowserModeChange: (mode: BrowserAutomationMode) => void
  cdpPort: number
  onCdpPortChange: (port: number) => void
  cdpConnected: boolean | null
  cdpError: string | null
  cdpChecking: boolean
  onCheckCdpConnection: (port: number) => void
  readOnly?: boolean
  dirty?: boolean
  saving?: boolean
  onSave?: () => void
  assistantControl?: ReactNode
  scopeNoun?: 'workflow' | 'project'
  profileId?: string
}

/** One inline project choice; account pairing remains reusable across projects. */
export function BrowserWorkspacePanel({
  workspacePath, browserMode, onBrowserModeChange, cdpPort, onCdpPortChange,
  cdpConnected, cdpError, cdpChecking, onCheckCdpConnection, readOnly = false,
  dirty = false, saving = false, onSave, assistantControl, scopeNoun = 'workflow', profileId,
}: BrowserWorkspacePanelProps) {
  const assistant = isValidElement<WorkspaceViewActionsProps>(assistantControl) && assistantControl.type === WorkspaceViewActions ? assistantControl : undefined
  useWorkspacePanelAssistant(assistant ? { workspacePath: assistant.props.workspacePath, message: assistant.props.message } : undefined)
  const headerActions = assistant ? cloneElement(assistant, { hideRefresh: true, walkthrough: null }) : assistantControl
  const extensionProfile = profileId || (workspacePath?.startsWith('Workflow/') ? 'workflow' : undefined)
  const extensionAvailable = ['work', 'workflow'].includes(extensionProfile || '') && !!workspacePath && !readOnly
  const connection = useChromeExtensionConnection(workspacePath, extensionProfile, !extensionAvailable)
  const useWorkspaceBrowser = async () => {
    if (connection.status.selected && !await connection.disconnect()) return
    onBrowserModeChange('headless')
  }
  const selection = !readOnly && workspacePath ? <div className="shrink-0 border-b border-border px-3 py-2">
    {extensionAvailable && <ChromeExtensionConnection connection={connection} workspaceBrowser={{ selected: !connection.status.selected, onSelect: useWorkspaceBrowser }} />}
    {isBrowserCDPEnabled() && <details className="mt-2 text-xs">
      <summary className="cursor-pointer text-muted-foreground">Advanced connection</summary>
      <div className="pt-3"><BrowserAutomationSettings allowAutomatic={profileId !== 'work'} browserMode={browserMode} onBrowserModeChange={onBrowserModeChange} cdpPort={cdpPort} onCdpPortChange={onCdpPortChange} cdpConnected={cdpConnected} cdpError={cdpError} cdpChecking={cdpChecking} onCheckCdpConnection={onCheckCdpConnection} readOnly={readOnly || connection.busy} scopeNoun={scopeNoun} /></div>
    </details>}
    {onSave && (dirty || saving) && <div className="mt-2 flex justify-end"><Button type="button" size="sm" disabled={!dirty || saving} onClick={onSave}>{saving ? 'Saving…' : 'Save browser choice'}</Button></div>}
  </div> : null
  const emptyBrowser = !readOnly && workspacePath ? ({ startBrowser, startingBrowser, canStart }: BrowserEmptyStateActions) => <div className="space-y-4 text-left" aria-label="Open a browser">
    <div className="space-y-2">
      <h3 className="text-base font-semibold">Your browser will appear here</h3>
      <p className="text-sm leading-relaxed text-muted-foreground">Open a browser or ask your agent to visit a website. You can both click and type here while it works.</p>
      {connection.status.account_connected && <p className="text-xs text-muted-foreground">Your saved browser is connected. Choose it above to use it in this {scopeNoun}.</p>}
    </div>
    {canStart && <Button type="button" disabled={startingBrowser || connection.busy} onClick={() => { void startBrowser() }} className="w-full gap-2">
      {startingBrowser ? <Loader2 className="h-4 w-4 animate-spin" /> : <Monitor className="h-4 w-4" />}
      {startingBrowser ? 'Opening browser…' : 'Open workspace browser'}
    </Button>}
    <p className="text-xs leading-relaxed text-muted-foreground">The workspace browser runs here. Saved Chrome and Edge browsers open their tabs on your computer.</p>
  </div> : undefined

  return <div className="relative flex h-full min-h-0 flex-1 flex-col">
    {connection.loading ? <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Checking browser connection…</div> : connection.status.selected ? <>
      <WorkspaceViewHeader icon={Monitor} title="Browser" subtitle={connection.status.browsers?.find(item => item.id === connection.status.browser_id)?.name || 'Saved browser'} showWalkthrough={false} actions={headerActions} />
      {selection}
      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        <div className="mx-auto my-6 max-w-sm space-y-4 text-center">
          <PlugZap className="mx-auto h-6 w-6 text-muted-foreground" />
          <p className="text-sm font-medium">{connection.status.connected ? 'Ready in your browser' : connection.status.browsers?.some(item => item.id === connection.status.browser_id && item.connected) ? 'Connecting this project…' : 'Selected browser is offline'}</p>
          <p className="text-xs leading-relaxed text-muted-foreground">{connection.status.connected ? 'You and your agent can use the project tabs in Chrome or Edge. Open that browser to follow along.' : connection.status.message || 'Open the selected browser to reconnect, or choose another browser above.'}</p>
          <span role="status" className="inline-flex items-center gap-2 text-xs"><span className={`h-2 w-2 rounded-full ${connection.status.connected ? 'bg-emerald-500' : 'bg-muted-foreground'}`} />{connection.status.connected ? `Connected · ${connection.status.tabs} project tabs` : 'Not ready for this project'}</span>
          {connection.status.connected && !!connection.status.tab_titles?.length && <ul className="space-y-1.5 text-left" aria-label="Project browser tabs">{connection.status.tab_titles.map((title, index) => <li key={index} title={title} className="truncate rounded-md border border-border px-3 py-2 text-xs">{title}</li>)}</ul>}
        </div>
      </div>
    </> : <WorkflowLiveBrowser workspacePath={workspacePath} scopeNoun={scopeNoun} profileId={profileId} emptyContent={emptyBrowser} browserSelection={selection} showGuide={false} toolbar={headerActions} />}
  </div>
}
