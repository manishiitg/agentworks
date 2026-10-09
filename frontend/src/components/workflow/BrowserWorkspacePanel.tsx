import { cloneElement, isValidElement, useState, useEffect, type ReactNode } from 'react'
import { ChromeExtensionConnection, useChromeExtensionConnection } from './ChromeExtensionConnection'
import { Button } from '../ui/Button'
import { Settings2, X, Monitor, PlugZap, Loader2, ArrowRight } from 'lucide-react'
import BrowserAutomationSettings, { type BrowserAutomationMode, type BrowserChoice } from '../BrowserAutomationSettings'
import { isBrowserCDPEnabled } from '../../utils/runtimeCapabilities'
import WorkflowLiveBrowser, { type BrowserEmptyStateActions } from './WorkflowLiveBrowser'
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
  const [extensionSetup, setExtensionSetup] = useState(false)
  const extensionProfile = profileId || (workspacePath?.startsWith('Workflow/') ? 'workflow' : undefined)
  const extensionAvailable = ['code', 'work', 'workflow'].includes(extensionProfile || '') && !!workspacePath && !readOnly
  const connection = useChromeExtensionConnection(workspacePath, extensionProfile, !extensionAvailable)
  const extensionView = connection.status.selected || extensionSetup
  const choice: BrowserChoice = extensionView ? 'extension' : browserMode
  useEffect(() => { setExtensionSetup(false); setSettingsOpen(false) }, [workspacePath, profileId])
  useEffect(() => {
    if (!settingsOpen) return
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setSettingsOpen(false) }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [settingsOpen])
  const changeChoice = async (next: BrowserChoice) => {
    if (next === 'extension') {
      setExtensionSetup(true)
      if (connection.status.account_connected && !connection.status.connected) await connection.connect()
      return
    }
    if (connection.status.selected && !await connection.disconnect()) return
    setExtensionSetup(false); onBrowserModeChange(next)
  }
  const otherBrowsers = extensionAvailable || isBrowserCDPEnabled() ? <div className="space-y-3 border-t border-border pt-4">
    {extensionAvailable && <div className="space-y-1">
      <p className="text-xs leading-relaxed text-muted-foreground">Need a website where you’re already signed in?</p>
      <Button type="button" variant="outline" disabled={connection.busy} onClick={() => { void changeChoice('extension').then(() => setSettingsOpen(true)) }} className="h-auto w-full justify-start gap-2 whitespace-normal py-2 text-left">
        <PlugZap className="h-4 w-4 shrink-0" aria-hidden="true" />Use my Chrome or Edge
        <ArrowRight className="ml-auto h-4 w-4 shrink-0" aria-hidden="true" />
      </Button>
      <p className="text-xs leading-relaxed text-muted-foreground">{connection.status.account_connected ? 'Your browser is connected to your account. Choose it to use it here.' : 'Requires the AgentWorks browser extension. We’ll help you connect it.'}</p>
    </div>}
    {isBrowserCDPEnabled() && <Button type="button" variant="ghost" disabled={connection.busy} onClick={() => { void changeChoice('cdp').then(() => setSettingsOpen(true)) }} className="h-auto whitespace-normal text-xs">Advanced browser connection</Button>}
  </div> : null
  const browserOptions = !readOnly && workspacePath ? <div className="space-y-4 text-left" aria-label="Switch browser">
    <Button type="button" variant="outline" disabled={connection.busy} onClick={() => { void changeChoice('headless').then(() => setSettingsOpen(false)) }} className="w-full">Use the built-in browser</Button>
    {otherBrowsers}
  </div> : undefined
  const emptyBrowser = !readOnly && workspacePath ? ({ startBrowser, startingBrowser, canStart }: BrowserEmptyStateActions) => <div className="space-y-5 text-left" aria-label="Open a browser">
    <div className="space-y-2">
      <h3 className="text-base font-semibold">Your browser will appear here</h3>
      <p className="text-sm leading-relaxed text-muted-foreground">When your helper browses a website or runs a browser test, you can watch it here.</p>
      <p className="text-xs leading-relaxed text-muted-foreground">You can also open a browser yourself to visit a website or show your helper what to do.</p>
    </div>
    {canStart && <div className="space-y-2">
      <Button type="button" disabled={startingBrowser || connection.busy} onClick={() => { void startBrowser() }} className="w-full gap-2">
        {startingBrowser ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : <Monitor className="h-4 w-4" aria-hidden="true" />}
        {startingBrowser ? 'Opening browser…' : 'Open browser'}
      </Button>
      <p className="text-xs leading-relaxed text-muted-foreground">Uses a separate browser for this {scopeNoun}. Sign in to websites here when needed.</p>
    </div>}
    {otherBrowsers}
  </div> : undefined
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
      {connection.loading ? <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Checking browser connection…</div> : extensionView ? <>
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-3 py-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary"><Monitor className="h-4 w-4" /></div>
            <div className="min-w-0"><h3 className="text-sm font-semibold">Browser</h3><p className="text-xs text-muted-foreground">Chrome or Edge extension</p></div>
          </div>
          <div className="flex items-center gap-2"><WorkspaceViewIconButton label="Browser settings" icon={Settings2} onClick={() => setSettingsOpen(value => !value)} />{guidedAssistantControl}</div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-6">
          <div className="mx-auto my-6 max-w-sm space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl bg-muted/60 text-muted-foreground"><PlugZap className="h-6 w-6" /></div>
            <div className="space-y-2"><p className="text-sm font-medium">{connection.status.connected ? connection.status.tabs > 0 ? 'Your browser is ready' : 'Ready for your agent' : connection.status.selected ? 'Browser disconnected' : 'Connect your browser'}</p>
              <p className="break-words text-xs text-muted-foreground">{(connection.status.workspace || workspacePath || '').split('/').filter(Boolean).pop()}</p>
              <p className="text-xs leading-relaxed text-muted-foreground">{connection.status.connected ? connection.status.tabs > 0 ? `Your agent can use ${connection.status.tabs} shared ${connection.status.tabs === 1 ? 'tab' : 'tabs'}. Watch its actions in your browser.` : 'Your agent can create and choose its own project tabs. Sharing an already-open page is optional.' : connection.status.selected ? 'Actions are paused. Reconnect the extension to continue.' : 'Connect the AgentWorks extension to use your signed-in Chrome or Edge tabs.'}</p>
            </div>
            <span className={`inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-[11px] ${connection.status.connected ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'border-border text-muted-foreground'}`}><span className={`h-1.5 w-1.5 rounded-full ${connection.status.connected ? 'bg-emerald-500' : 'bg-muted-foreground'}`} />{connection.status.connected ? `Connected · ${connection.status.tabs} shared ${connection.status.tabs === 1 ? 'tab' : 'tabs'}` : connection.status.selected ? 'Disconnected' : 'Not connected'}</span>
            {connection.status.connected && connection.status.tab_titles?.length ? <ul className="space-y-1.5 text-left" aria-label="Shared browser tabs">{connection.status.tab_titles.map((title, index) => <li key={index} title={title} className="truncate rounded-md border border-border bg-muted/20 px-3 py-2 text-xs">{title}</li>)}</ul> : null}
            <div><Button size="sm" variant="outline" onClick={() => setSettingsOpen(true)}>{connection.status.connected ? 'Connection settings' : 'Set up connection'}</Button></div>
            {!connection.status.connected && browserOptions}
          </div>
        </div>
      </> : <WorkflowLiveBrowser workspacePath={workspacePath} scopeNoun={scopeNoun} profileId={profileId} onLearn={onLearn} emptyContent={emptyBrowser} showGuide={false} toolbar={<>
        <WorkspaceViewIconButton label="Browser settings" icon={Settings2} onClick={() => setSettingsOpen(value => !value)} />
        {guidedAssistantControl}
      </>} />}

      {settingsOpen && (
        <div
          role="dialog"
          aria-label="Browser settings"
          className="absolute left-2 right-2 top-12 z-[60] ml-auto max-w-md max-h-[calc(100%_-_4rem)] overflow-y-auto rounded-xl border border-border bg-background p-5 shadow-xl"
        >
          <div className="mb-3 flex items-center justify-between">
            <div><h3 className="text-sm font-semibold">Browser settings</h3><p className="mt-0.5 text-[11px] text-muted-foreground">Choose where your agent browses.</p></div>
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
            browserChoice={choice}
            onBrowserChoiceChange={next => { void changeChoice(next) }}
            allowAutomatic={profileId !== 'code'}
            extensionAvailable={extensionAvailable}
            extensionConnected={connection.status.account_connected}
            extensionContent={<ChromeExtensionConnection connection={connection} />}
            cdpPort={cdpPort}
            onCdpPortChange={onCdpPortChange}
            cdpConnected={cdpConnected}
            cdpError={cdpError}
            cdpChecking={cdpChecking}
            onCheckCdpConnection={onCheckCdpConnection}
            readOnly={readOnly || connection.busy}
            scopeNoun={scopeNoun}
          />
          {!readOnly && onSave && choice !== 'extension' && (
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
