import { cloneElement, isValidElement, useState, type ReactNode } from 'react'
import { ChromeExtensionConnection } from './ChromeExtensionConnection'
import { Button } from '../ui/Button'
import { Settings2, X } from 'lucide-react'
import BrowserAutomationSettings, { type BrowserAutomationMode } from '../BrowserAutomationSettings'
import WorkflowLiveBrowser from './WorkflowLiveBrowser'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { WorkspaceViewActions, type WorkspaceViewActionsProps } from './WorkspaceViewActions'
import { WorkspacePanelGuideButton } from './WorkspacePanelGuideButton'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

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

/**
 * Shared AgentWorks/Work browser view. Keeping the live browser and its
 * settings overlay together prevents product surfaces from inventing a
 * second place to configure browser access.
 */
export function BrowserWorkspacePanel({
  workspacePath,
  browserMode,
  onBrowserModeChange,
  cdpPort,
  onCdpPortChange,
  cdpConnected,
  cdpError,
  cdpChecking,
  onCheckCdpConnection,
  readOnly = false,
  dirty = false,
  saving = false,
  onSave,
  assistantControl,
  scopeNoun = 'workflow',
  profileId,
}: BrowserWorkspacePanelProps) {
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [extensionSelected, setExtensionSelected] = useState(false)
  const walkthrough = <WorkspacePanelGuideButton topic="Browser" />
  const guidedAssistantControl = isValidElement<WorkspaceViewActionsProps>(assistantControl) && assistantControl.type === WorkspaceViewActions
    ? cloneElement(assistantControl, {
      walkthrough: cloneElement(walkthrough, {
        ask: {
          workspacePath: assistantControl.props.workspacePath,
          message: assistantControl.props.message,
          onAsk: assistantControl.props.onAsk,
        },
      }),
    })
    : <>{assistantControl}{walkthrough}</>

  const learn = isValidElement<WorkspaceViewActionsProps>(assistantControl) ? assistantControl.props.onAsk : undefined
  const onLearn = learn ?? (workspacePath ? (message: string) => sendWorkspacePaneMessageToChat({ workspacePath, message }) : undefined)

  return (
    <div className="relative flex h-full min-h-0 flex-1 flex-col">
      <ChromeExtensionConnection workspacePath={workspacePath} profileId={profileId} readOnly={readOnly} onSelectionChange={setExtensionSelected} />
      {extensionSelected ? <div className="flex min-h-0 flex-1 items-center justify-center p-6 text-center text-sm text-muted-foreground"><p>Your agent uses the tabs shared from your Chrome extension.<br />Watch and control those tabs in Chrome.</p></div> : <WorkflowLiveBrowser workspacePath={workspacePath} scopeNoun={scopeNoun} profileId={profileId} onLearn={onLearn} showGuide={false} toolbar={<>
        <WorkspaceViewIconButton label="Browser settings" icon={Settings2} onClick={() => setSettingsOpen(value => !value)} />
        {guidedAssistantControl}
      </>} />}
      {settingsOpen && (
        <div
          role="dialog"
          aria-label="Browser settings"
          className="absolute left-2 right-2 top-12 z-10 ml-auto max-w-lg max-h-[calc(100%-4rem)] overflow-y-auto rounded-lg border border-border bg-background p-4 shadow-xl"
        >
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-medium">Browser settings</h3>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Close browser settings"
              onClick={() => setSettingsOpen(false)}
              className="h-7 w-7"
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
          <BrowserAutomationSettings
            browserMode={browserMode}
            onBrowserModeChange={onBrowserModeChange}
            cdpPort={cdpPort}
            onCdpPortChange={onCdpPortChange}
            cdpConnected={cdpConnected}
            cdpError={cdpError}
            cdpChecking={cdpChecking}
            onCheckCdpConnection={onCheckCdpConnection}
            readOnly={readOnly}
            scopeNoun={scopeNoun}
          />
          {!readOnly && onSave && (
            <div className="mt-3 flex items-center justify-end gap-3 border-t pt-3">
              {dirty && <span className="text-xs text-muted-foreground">Unsaved changes</span>}
              <Button
                type="button"
                size="sm"
                disabled={!dirty || saving}
                onClick={onSave}
              >
                {saving ? 'Saving…' : 'Save settings'}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
