import React from 'react'
import {
  Check,
  ChevronDown,
  Copy,
  Download,
  ExternalLink,
  Loader2,
  Monitor,
  MonitorOff,
  RefreshCw,
  Sparkles,
} from 'lucide-react'

import {
  chromeCdpInstallCommand,
  chromeCdpLaunchCommand,
  chromeCdpVerifyCommand,
  chromeCdpZipUrl,
} from '../utils/cdpSetup'
import { isBrowserCDPEnabled } from '../utils/runtimeCapabilities'
import { Button } from './ui/Button'
import { Input } from './ui/Input'

export type BrowserAutomationMode = 'none' | 'auto' | 'headless' | 'cdp'

interface BrowserAutomationSettingsProps {
  browserMode: BrowserAutomationMode
  onBrowserModeChange: (mode: BrowserAutomationMode) => void
  cdpPort: number
  onCdpPortChange: (port: number) => void
  cdpConnected: boolean | null
  cdpError: string | null
  cdpChecking: boolean
  onCheckCdpConnection: (port: number) => void
  readOnly?: boolean
  scopeNoun?: 'workflow' | 'project'
}

interface CommandBlockProps {
  command: string
  label: string
}

const CommandBlock: React.FC<CommandBlockProps> = ({ command, label }) => {
  const [copied, setCopied] = React.useState(false)

  const copyCommand = async () => {
    try {
      await navigator.clipboard.writeText(command)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-medium text-gray-600 dark:text-gray-300">{label}</span>
        <Button
          type="button"
          variant="link"
          size="sm"
          onClick={copyCommand}
          aria-label={`Copy ${label.toLowerCase()}`}
        >
          {copied ? <Check className="h-3 w-3 text-emerald-500" /> : <Copy className="h-3 w-3" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <div className="w-full min-w-0 max-w-full overflow-x-auto rounded-md border border-gray-200 bg-gray-950 dark:border-gray-700">
        <pre className="w-max min-w-full whitespace-pre px-3 py-2.5 pr-8 text-[11px] leading-relaxed text-cyan-300"><code>{command}</code></pre>
      </div>
    </div>
  )
}

const BrowserAutomationSettings: React.FC<BrowserAutomationSettingsProps> = ({
  browserMode,
  onBrowserModeChange,
  cdpPort,
  onCdpPortChange,
  cdpConnected,
  cdpError,
  cdpChecking,
  onCheckCdpConnection,
  readOnly = false,
  scopeNoun = 'workflow',
}) => {
  const platform = typeof navigator !== 'undefined' ? navigator.platform : undefined
  const isMac = platform?.includes('Mac')
  const cdpEnabled = isBrowserCDPEnabled()
  const usesCdp = cdpEnabled && (browserMode === 'auto' || browserMode === 'cdp')

  const connectionLabel = cdpChecking
    ? 'Checking'
    : cdpConnected === true
      ? 'Connected'
      : cdpConnected === false
        ? 'Not connected'
        : 'Not checked'

  const connectionStyle = cdpChecking
    ? 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
    : cdpConnected === true
      ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
      : cdpConnected === false
        ? 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300'
        : 'border-gray-500/20 bg-gray-500/10 text-gray-500 dark:text-gray-400'

  return (
    <section className="space-y-3" aria-labelledby="browser-automation-heading">
      <div>
        <div>
          <h3 id="browser-automation-heading" className="text-sm font-medium text-gray-900 dark:text-gray-100">
            Browser Automation
          </h3>
          <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {cdpEnabled
              ? `This saves the ${scopeNoun} policy. Chrome availability is checked live each time the ${scopeNoun} runs.`
              : 'CDP is disabled on this server. Automatic and Headless use managed background Chromium.'}
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className={`flex min-h-24 cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors ${
          browserMode === 'auto'
            ? 'border-cyan-500 bg-cyan-500/10'
            : 'border-gray-200 hover:bg-gray-500/5 dark:border-gray-700'
        }`}>
          <input type="radio" name="presetBrowserMode" checked={browserMode === 'auto'} disabled={readOnly} onChange={() => onBrowserModeChange('auto')} className="mt-0.5 h-4 w-4 accent-cyan-500" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-gray-100">
              <Sparkles className="h-3.5 w-3.5 text-cyan-500" />
              Automatic
              <span className="text-[10px] font-medium uppercase tracking-wide text-cyan-600 dark:text-cyan-400">Default · Recommended</span>
            </div>
            <p className="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
              {cdpEnabled
                ? 'Uses visible Chrome when CDP is reachable; otherwise uses managed headless Chromium.'
                : 'Uses managed headless Chromium on this server; CDP probing is disabled.'}
            </p>
          </div>
        </label>

        {cdpEnabled && <label className={`flex min-h-24 items-start gap-3 rounded-lg border p-3 transition-colors ${
          !cdpEnabled ? 'cursor-not-allowed opacity-60 ' : 'cursor-pointer '
        }${
          browserMode === 'cdp'
            ? 'border-emerald-500 bg-emerald-500/10'
            : 'border-gray-200 hover:bg-gray-500/5 dark:border-gray-700'
        }`}>
          <input type="radio" name="presetBrowserMode" checked={browserMode === 'cdp'} disabled={readOnly || !cdpEnabled} onChange={() => onBrowserModeChange('cdp')} className="mt-0.5 h-4 w-4 accent-emerald-500" />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-gray-100">
              <Monitor className="h-3.5 w-3.5 text-emerald-500" />
              Require visible Chrome
              {!cdpEnabled && <span className="rounded bg-gray-500/15 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-gray-500">Disabled on server</span>}
            </div>
            <p className="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
              {cdpEnabled
                ? 'Requires CDP and stops with a clear error if that Chrome is unavailable.'
                : `Unavailable in this deployment. A ${scopeNoun} cannot force or configure CDP here.`}
            </p>
          </div>
        </label>}

        <label className={`flex min-h-24 cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors ${
          browserMode === 'headless'
            ? 'border-blue-500 bg-blue-500/10'
            : 'border-gray-200 hover:bg-gray-500/5 dark:border-gray-700'
        }`}>
          <input type="radio" name="presetBrowserMode" checked={browserMode === 'headless'} disabled={readOnly} onChange={() => onBrowserModeChange('headless')} className="mt-0.5 h-4 w-4 accent-blue-500" />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-gray-100">
              <MonitorOff className="h-3.5 w-3.5 text-blue-500" />
              Managed browser
            </div>
            <p className="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
              Uses this workspace’s browser. Open its live view to sign in or take control.
            </p>
          </div>
        </label>


      </div>

      {!cdpEnabled && browserMode === 'cdp' && (
        <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:text-amber-200">
          This {scopeNoun} has a legacy CDP setting, but CDP is disabled on this server. Select Automatic or Managed browser before saving.
        </p>
      )}

      {usesCdp && (
        <div className="overflow-hidden rounded-lg border border-gray-200 bg-gray-50 dark:border-gray-700 dark:bg-gray-900/60">
          <div className="space-y-3 p-3 sm:p-4">
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div>
                <h4 className="text-sm font-medium text-gray-900 dark:text-gray-100">Local Chrome connection</h4>
                <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {browserMode === 'auto'
                    ? 'Automatic mode rechecks this port at run time and falls back to headless when it is unavailable.'
                    : 'This mode requires Chrome to remain reachable on the saved port.'}
                </p>
              </div>
              <span className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-[11px] font-medium ${connectionStyle}`}>
                {cdpChecking ? <Loader2 className="h-3 w-3 animate-spin" /> : <span className="h-1.5 w-1.5 rounded-full bg-current" />}
                {connectionLabel}
              </span>
            </div>

            <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
              <label className="block sm:w-40">
                <span className="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">Chrome CDP port</span>
                <Input
                  type="number"
                  value={cdpPort}
                  disabled={readOnly}
                  onChange={(event) => onCdpPortChange(parseInt(event.target.value, 10) || 9222)}
                  min={1}
                  max={65535}
                  aria-label="Chrome CDP port"
                />
              </label>
              <Button
                type="button"
                variant="outline"
                onClick={() => onCheckCdpConnection(cdpPort)}
                disabled={cdpChecking}
              >
                {cdpChecking ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                Check now
              </Button>
            </div>

            {cdpConnected === false && (
              <p className="text-xs text-red-600 dark:text-red-300">
                Chrome is not reachable on port {cdpPort}.{cdpError ? ` ${cdpError}` : ''}
              </p>
            )}

            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:text-amber-200">
              {scopeNoun === 'workflow'
                ? 'Visible Chrome can take keyboard focus. For schedules, prefer Automatic or Managed browser, or launch a dedicated Chrome profile on this port.'
                : 'Visible Chrome can take keyboard focus. Prefer Automatic or Managed browser, or launch a dedicated Chrome profile on this port.'}
            </p>
          </div>

          <details className="group min-w-0 border-t border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900/70">
            <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-3 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-gray-800/60 sm:px-4 [&::-webkit-details-marker]:hidden">
              <span>Need to install or launch Chrome with CDP?</span>
              <ChevronDown className="h-4 w-4 shrink-0 text-gray-400 transition-transform group-open:rotate-180" />
            </summary>
            <div className="grid min-w-0 gap-5 border-t border-gray-200 p-3 dark:border-gray-700 sm:p-4 2xl:grid-cols-2">
              {isMac && (
                <div className="min-w-0 space-y-3">
                  <div>
                    <h5 className="text-xs font-semibold text-gray-800 dark:text-gray-100">Recommended on macOS</h5>
                    <p className="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
                      The installer updates the app, clears quarantine, signs it locally, opens it, and verifies port {cdpPort}.
                    </p>
                  </div>
                  <CommandBlock label="Install or update" command={chromeCdpInstallCommand(cdpPort)} />
                  <a
                    href={chromeCdpZipUrl}
                    download="Chrome-CDP-macOS.zip"
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(event) => event.stopPropagation()}
                    className="inline-flex items-center gap-1.5 rounded-md bg-emerald-600 px-3 py-2 text-xs font-medium text-white transition-colors hover:bg-emerald-500"
                  >
                    <Download className="h-3.5 w-3.5" />
                    Download Chrome CDP.app
                  </a>
                  <p className="text-xs leading-relaxed text-gray-500 dark:text-gray-400">
                    Manual install: unzip, move the app to Applications, then open it. If macOS blocks it, allow it in Privacy &amp; Security or run <code className="rounded bg-gray-100 px-1 py-0.5 font-mono text-[11px] dark:bg-gray-800">xattr -c /Applications/Chrome\ CDP.app</code>.
                  </p>
                </div>
              )}

              <div className="min-w-0 space-y-3">
                <div>
                  <h5 className="text-xs font-semibold text-gray-800 dark:text-gray-100">Launch manually</h5>
                  <p className="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
                    Uses a dedicated profile. A different port automatically gets a different profile for independent logins.
                  </p>
                </div>
                <CommandBlock label="Launch Chrome" command={chromeCdpLaunchCommand(cdpPort, platform)} />
                <CommandBlock label="Verify the port" command={chromeCdpVerifyCommand(cdpPort)} />
                <a
                  href="https://github.com/manishiitg/coding-agent-loop#chrome-cdp-browser"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-xs font-medium text-cyan-700 hover:text-cyan-600 dark:text-cyan-400"
                >
                  Full browser setup guide <ExternalLink className="h-3 w-3" />
                </a>
              </div>
            </div>
          </details>
        </div>
      )}
    </section>
  )
}

export default BrowserAutomationSettings
