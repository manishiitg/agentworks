import { useEffect, useState } from 'react'
import { Check, Copy, Download, Loader2 } from 'lucide-react'
import { chromeCdpInstallCommand, chromeCdpLaunchCommand, chromeCdpZipUrl } from '../utils/cdpSetup'
import { Button } from './ui/Button'
import { Input } from './ui/Input'

interface BrowserAutomationSettingsProps {
  cdpPort?: number
  onCdpPortChange?: (port: number) => void
  cdpConnected: boolean | null
  cdpError: string | null
  cdpChecking: boolean
  onCheckCdpConnection: (port: number) => void
  readOnly?: boolean
}

export type BrowserAutomationMode = 'none' | 'extension' | 'headless' | 'cdp' | undefined

export default function BrowserAutomationSettings({ cdpPort = 9222, onCdpPortChange, cdpConnected, cdpError, cdpChecking, onCheckCdpConnection, readOnly = false }: BrowserAutomationSettingsProps) {
  const [copied, setCopied] = useState(false)
  const isMac = navigator.platform?.includes('Mac')
  const command = isMac ? chromeCdpInstallCommand(cdpPort) : chromeCdpLaunchCommand(cdpPort, navigator.platform)
  useEffect(() => { setCopied(false) }, [command])
  // Only the explicitly opened setup checks for the installer completing.
  useEffect(() => {
    if (cdpConnected || cdpChecking || readOnly) return
    const timer = window.setTimeout(() => onCheckCdpConnection(cdpPort), 3000)
    return () => window.clearTimeout(timer)
  }, [cdpConnected, cdpChecking, cdpPort, onCheckCdpConnection, readOnly])
  async function copyCommand() {
    try { await navigator.clipboard.writeText(command); setCopied(true) } catch { setCopied(false) }
  }
  return <section className="space-y-3 rounded-md border border-border p-3" aria-label="Chrome Direct setup">
    <div className="flex items-center justify-between gap-2"><h3 className="text-xs font-medium">Set up Chrome Direct</h3><span role="status" className="text-xs text-muted-foreground">{cdpChecking ? 'Checking…' : cdpConnected ? 'Ready' : 'Not connected'}</span></div>
    <p className="text-xs text-muted-foreground">{isMac ? 'Run the installer once. It opens Chrome with a separate profile.' : 'Launch Chrome with a separate profile using this command.'}</p>
    <pre className="overflow-x-auto rounded bg-muted/40 p-2 text-[11px]"><code>{command}</code></pre>
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" onClick={() => void copyCommand()}>{copied ? <Check /> : <Copy />}{copied ? 'Copied' : isMac ? 'Copy install command' : 'Copy launch command'}</Button>
      {isMac && <Button size="sm" variant="outline" asChild><a href={chromeCdpZipUrl} download="Chrome-CDP-macOS.zip" target="_blank" rel="noopener noreferrer"><Download />Download app</a></Button>}
      <Button size="sm" variant="ghost" disabled={cdpChecking || readOnly} onClick={() => onCheckCdpConnection(cdpPort)}>{cdpChecking && <Loader2 className="animate-spin" />}Check connection</Button>
    </div>
    {cdpError && cdpConnected === false && <p role="alert" className="text-xs text-destructive">{cdpError}</p>}
    <details className="text-xs"><summary className="cursor-pointer text-muted-foreground">Connection details</summary><div className="space-y-2 pt-2">
      {onCdpPortChange && <label className="flex items-center gap-2">Port<Input className="w-24" type="number" min={1} max={65535} aria-label="Chrome CDP port" value={cdpPort} disabled={readOnly} onChange={event => onCdpPortChange(Number(event.target.value) || 9222)} /></label>}
      <p className="text-muted-foreground">Chrome must stay open on this computer. Each port uses its own profile.</p>
      {isMac && <p className="text-muted-foreground">The installer updates and signs the launcher locally, clears quarantine and checks the port. macOS may ask you to allow the app.</p>}
    </div></details>
  </section>
}
