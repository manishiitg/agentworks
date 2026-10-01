import { useState } from 'react'
import { Check, Copy, Loader2, PlugZap } from 'lucide-react'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Button } from '../../components/ui/Button'
import { codeClass } from './gatewayConsoleUtils'

/**
 * Connect-client page: the workspace MCP endpoint, how to sign into it,
 * and a credential-free reachability check (an unauthenticated probe must
 * answer 401 with the OAuth challenge, proving the endpoint is alive).
 */
export function GatewayConnectPanel({ base }: { base: string }) {
  const endpoint = `${base}/mcp`
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState('')
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null)

  async function onCopy() {
    setCopied(false)
    setCopyError('')
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(endpoint)
      setCopied(true)
    } catch {
      setCopyError('Could not copy. Select the endpoint URL above and copy it manually.')
    }
  }

  async function onTest() {
    setTesting(true)
    setResult(null)
    try {
      const resp = await fetch(endpoint, { headers: { Accept: 'application/json' } })
      const challenge = resp.headers.get('WWW-Authenticate') ?? ''
      if (resp.status === 401 && challenge.includes('oauth-protected-resource')) {
        setResult({ ok: true, text: 'Endpoint reachable — sign-in required. Connect your client below.' })
      } else {
        setResult({ ok: false, text: `Unexpected answer (HTTP ${resp.status}). Is this URL a gateway MCP endpoint?` })
      }
    } catch {
      setResult({ ok: false, text: 'Endpoint unreachable. Start the gateway backend and retry.' })
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="space-y-4" data-testid="gateway-connect">
      <SettingsCard
        icon={<PlugZap className="h-4 w-4 text-primary" />}
        title="Workspace MCP endpoint"
        description="Use OAuth or a group API key."
      >
        <p className="flex flex-wrap items-center gap-2">
          <code className="break-all rounded bg-muted px-2 py-1 font-mono text-xs" data-testid="gateway-connect-url">
            {endpoint}
          </code>
          <Button variant="outline" size="xs" onClick={() => void onCopy()} data-testid="gateway-connect-copy">
            {copied ? <Check /> : <Copy />}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </p>
        {copyError && <p role="alert" className="text-destructive">{copyError}</p>}
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="xs" disabled={testing} onClick={() => void onTest()} data-testid="gateway-connect-test">
            {testing && <Loader2 className="animate-spin" />}
            Send test request
          </Button>
          {result && (
            <span role="status" className={result.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}>
              {result.text}
            </span>
          )}
        </div>
      </SettingsCard>

      <SettingsCard title="Connect Claude" description="OAuth sign-in.">
        <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
          <li>Paste the endpoint URL above as a custom connector.</li>
          <li>Sign in with your AgentWorks account.</li>
          <li>You see only the tools your groups grant you.</li>
        </ol>
      </SettingsCard>

      <SettingsCard
        title="Connect with an API key"
        description="For clients without OAuth."
      >
        <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
          <li>Create a key on the Groups page. Copy it now — it is shown once.</li>
          <li>
            Send it as <span className={codeClass}>Authorization: Bearer &lt;key&gt;</span> against the endpoint URL.
          </li>
          <li>The key carries exactly its group&apos;s permissions — nothing else.</li>
        </ol>
      </SettingsCard>
    </div>
  )
}
