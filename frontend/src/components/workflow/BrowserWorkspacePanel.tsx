import { useState } from 'react'
import { ChromeExtensionConnection, useChromeExtensionConnection } from './ChromeExtensionConnection'
import { Button } from '../ui/Button'
import { Check, Loader2 } from 'lucide-react'
import BrowserAutomationSettings, { type BrowserAutomationMode } from '../BrowserAutomationSettings'
import WorkflowLiveBrowser, { type BrowserEmptyStateActions } from './WorkflowLiveBrowser'
import { BrowserScriptSetup } from './BrowserScriptSetup'

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
  onSendToAgent?: (message: string) => Promise<unknown>
}

/** Browser configuration and observing a running session are separate actions. */
export function BrowserWorkspacePanel({ workspacePath, browserMode, onBrowserModeChange, cdpPort = 9222, onCdpPortChange, cdpConnected, cdpError, cdpChecking, onCheckCdpConnection, readOnly = false, dirty = false, saving = false, onSave, scopeNoun = 'workflow', profileId, onSendToAgent }: BrowserWorkspacePanelProps) {
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
  const overview = configurable ? (actions: BrowserEmptyStateActions) => <>
    {connection.loading ? <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Checking browser connections…</p> : <ChromeExtensionConnection key={`${extensionProfile}:${workspacePath}`} connection={connection} additionalBrowsers={<div className="space-y-1">
      {modes.includes('cdp') && <div>
        <div className="flex min-w-0 items-center gap-2 rounded px-2 py-1.5 text-xs"><span className="flex min-w-0 flex-1 items-center gap-2">Chrome Direct{selected('cdp') && <Check className="h-3.5 w-3.5" aria-label="Selected" />}</span><Button size="xs" variant="ghost" onClick={() => setCdpSetupOpen(value => !value)} aria-expanded={cdpSetupOpen}>{cdpConnected ? 'Details' : 'Set up'}</Button>{cdpConnected && <Button size="xs" variant="ghost" aria-label={selected('cdp') ? 'Open Chrome Direct' : 'Use Chrome Direct'} disabled={connection.busy || actions.startingBrowser || !actions.canStart} onClick={() => { if (selected('cdp')) void actions.startBrowser(); else void useBrowser('cdp') }}>{selected('cdp') ? 'Open' : 'Use'}</Button>}</div>
        {cdpSetupOpen && <div className="mt-2 space-y-2"><BrowserAutomationSettings cdpPort={cdpPort} onCdpPortChange={onCdpPortChange} cdpConnected={cdpConnected} cdpError={cdpError} cdpChecking={cdpChecking} onCheckCdpConnection={onCheckCdpConnection} readOnly={connection.busy} />{onSave && (dirty || saving) && <Button size="sm" disabled={saving} onClick={onSave}>{saving ? 'Saving…' : 'Apply connection settings'}</Button>}</div>}
      </div>}
      {modes.includes('headless') && <div className="flex min-w-0 items-center gap-2 rounded px-2 py-1.5 text-xs"><span className="flex min-w-0 flex-1 items-center gap-2">Private browser{selected('headless') && <Check className="h-3.5 w-3.5" aria-label="Selected" />}</span><Button size="xs" variant="ghost" aria-label={selected('headless') ? 'Open private browser' : 'Use private browser'} disabled={connection.busy || actions.startingBrowser || !actions.canStart} onClick={() => { if (selected('headless')) void actions.startBrowser(); else void useBrowser('headless') }}>{actions.startingBrowser ? 'Opening…' : selected('headless') ? 'Open' : 'Use'}</Button></div>}
    </div>} />}
  </> : undefined

  return <div className="flex h-full min-h-0 flex-1 flex-col"><WorkflowLiveBrowser key={workspacePath} workspacePath={workspacePath} scopeNoun={scopeNoun} profileId={profileId} overview={overview} overviewFooter={configurable ? <BrowserScriptSetup onSendToAgent={onSendToAgent} /> : undefined} /></div>
}
