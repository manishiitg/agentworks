import { useState } from 'react'
import { ChromeExtensionConnection, useChromeExtensionConnection } from './ChromeExtensionConnection'
import { Button } from '../ui/Button'
import { Loader2 } from 'lucide-react'
import BrowserAutomationSettings, { type BrowserAutomationMode } from '../BrowserAutomationSettings'
import WorkflowLiveBrowser, { type BrowserEmptyStateActions } from './WorkflowLiveBrowser'
import { BrowserScriptSetup } from './BrowserScriptSetup'

interface BrowserWorkspacePanelProps {
  chatContextTabId?: string
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
  onSendToAgent?: (message: string) => Promise<unknown>
}

/** Browser configuration and observing a running session are separate actions. */
export function BrowserWorkspacePanel({ chatContextTabId, workspacePath, browserMode, onBrowserModeChange, cdpPort = 9222, onCdpPortChange, cdpConnected, cdpError, cdpChecking, onCheckCdpConnection, readOnly = false, dirty = false, saving = false, onSave, scopeNoun = 'workflow', profileId, onSendToAgent }: BrowserWorkspacePanelProps) {
  const [cdpSetupOpen, setCdpSetupOpen] = useState(false)
  const extensionProfile = profileId || (workspacePath?.startsWith('Workflow/') ? 'workflow' : undefined)
  const configurable = ['work', 'workflow'].includes(extensionProfile || '') && !!workspacePath && !readOnly
  const connection = useChromeExtensionConnection(workspacePath, extensionProfile, !configurable)
  const displayMode = connection.status.mode || browserMode
  const modes = connection.status.allowed_modes || []
  const selected = (mode: 'cdp' | 'headless') => !connection.status.selected && displayMode === mode
  async function useBrowser(mode: 'cdp' | 'headless') {
    if (!await connection.selectWorkspace(mode, cdpPort)) return
    onBrowserModeChange(mode)
  }
  // The server's allowed modes decide which other ways show: "cdp" is AgentWorks' own Chrome (a Mac), "headless" the
  // browser on the server. The screen only names them (owner, 11 Nov: no "Chrome Direct", no "Private browser").
  const mac = modes.includes('cdp')
  const ownChrome = (actions: BrowserEmptyStateActions) => <div className="rounded-lg border border-border p-3">
    <div className="flex items-start gap-3">
      <div className="min-w-0 flex-1"><p className="text-sm font-medium">Let AgentWorks open its own Chrome</p><p className="mt-0.5 text-xs text-muted-foreground">A separate Chrome window just for agents, with no sign-ins of yours. Use it when you don't want the extension, or want agents kept apart from your browsing.</p></div>
      {cdpConnected && <Button size="xs" variant="outline" aria-label={selected('cdp') ? 'Open AgentWorks’ own Chrome' : 'Use AgentWorks’ own Chrome'} disabled={connection.busy || actions.startingBrowser || !actions.canStart} onClick={() => { if (selected('cdp')) void actions.startBrowser(); else void useBrowser('cdp') }}>{selected('cdp') ? 'Open' : 'Use'}</Button>}
      {!cdpConnected && <Button size="xs" variant="outline" onClick={() => setCdpSetupOpen(value => !value)} aria-expanded={cdpSetupOpen}>Set up</Button>}
    </div>
    <button type="button" className="mt-2 text-xs text-muted-foreground hover:text-foreground" aria-expanded={cdpSetupOpen} onClick={() => setCdpSetupOpen(value => !value)}>Advanced</button>
    {cdpSetupOpen && <div className="mt-2 space-y-2"><BrowserAutomationSettings cdpPort={cdpPort} onCdpPortChange={onCdpPortChange} cdpConnected={cdpConnected} cdpError={cdpError} cdpChecking={cdpChecking} onCheckCdpConnection={onCheckCdpConnection} readOnly={connection.busy} />{onSave && (dirty || saving) && <Button size="sm" disabled={saving} onClick={onSave}>{saving ? 'Saving…' : 'Apply connection settings'}</Button>}</div>}
  </div>
  const serverBrowser = (actions: BrowserEmptyStateActions) => <div className="rounded-lg border border-border p-3"><div className="flex items-start gap-3">
    <div className="min-w-0 flex-1"><p className="text-sm font-medium">Browser on the server</p><p className="mt-0.5 text-xs text-muted-foreground">Agents use a browser that runs on the server. You can watch it here. Good for jobs that run while you're away; it has none of your sign-ins.</p></div>
    <Button size="xs" variant="outline" aria-label={selected('headless') ? 'Open the browser on the server' : 'Use the browser on the server'} disabled={connection.busy || actions.startingBrowser || !actions.canStart} onClick={() => { if (selected('headless')) void actions.startBrowser(); else void useBrowser('headless') }}>{actions.startingBrowser ? 'Opening…' : selected('headless') ? 'Open' : 'Use'}</Button>
  </div></div>
  // The mode in use when it is not a Chrome extension: its own card first, with the way back to the user's own Chrome.
  const currentOwn = (actions: BrowserEmptyStateActions) => {
    if (displayMode !== 'cdp' && displayMode !== 'headless') return undefined
    if (connection.status.selected) return undefined
    const own = displayMode === 'cdp'
    return <div className="space-y-2">
      <div className="rounded-lg border border-border p-3">
        <div className="flex items-center gap-2"><span aria-hidden="true" className="h-2 w-2 rounded-full bg-emerald-500" /><span className="text-sm font-medium">{own ? 'AgentWorks’ own Chrome' : 'Browser on the server'}</span></div>
        <p className="mt-0.5 text-xs text-muted-foreground">{own ? 'A separate window for agents. Your own Chrome isn’t used.' : 'A browser that runs on the server. Your own Chrome isn’t used.'}</p>
        <div className="mt-2"><Button size="xs" variant="outline" disabled={connection.busy || actions.startingBrowser || !actions.canStart} onClick={() => void actions.startBrowser()}>{actions.startingBrowser ? 'Opening…' : 'Open'}</Button></div>
      </div>
      <p className="rounded-r-md border-l-2 border-primary bg-muted/30 px-3 py-1.5 text-xs text-muted-foreground">Want agents to use your sign-ins? <b className="text-foreground">Use your own Chrome instead</b>: add the extension below.</p>
    </div>
  }
  const overview = configurable ? (actions: BrowserEmptyStateActions) => <>
    {connection.loading ? <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Checking browser connections…</p> : <ChromeExtensionConnection key={`${extensionProfile}:${workspacePath}`} connection={connection} mac={mac} ownCard={currentOwn(actions)} otherWays={modes.includes('cdp') || modes.includes('headless') ? <>{modes.includes('cdp') && ownChrome(actions)}{modes.includes('headless') && serverBrowser(actions)}</> : undefined} />}
  </> : undefined

  return <div className="flex h-full min-h-0 flex-1 flex-col"><WorkflowLiveBrowser key={workspacePath} chatContextTabId={chatContextTabId} workspacePath={workspacePath} scopeNoun={scopeNoun} profileId={profileId} overview={overview} overviewFooter={configurable ? <BrowserScriptSetup onSendToAgent={onSendToAgent} /> : undefined} /></div>
}
