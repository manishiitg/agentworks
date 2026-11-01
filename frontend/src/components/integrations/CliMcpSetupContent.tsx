import { useEffect, useState } from 'react'
import { Copy, Download, Globe, Plug } from 'lucide-react'
import { SettingsCard } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { useAuthStore } from '../../stores/useAuthStore'
import { HostedAiAppGuide } from './HostedAiAppGuide'
import { AI_APPS, AiAppPicker, isHostedAiApp, type AiApp, type HostedAiApp } from './AiAppPicker'
import { LocalMcpTokenPanel } from './LocalMcpTokenPanel'
import { CommandRow, CopyButton } from './LocalAgentSetupSteps'
import api, { externalSkillApi, getApiBaseUrl } from '../../services/api'

type Destination = 'local-assistant' | 'hosted-assistant'
type LocalClient = 'claude-code' | 'codex' | 'cursor' | 'muse' | 'json-client'
type HostedClient = HostedAiApp
interface OAuthConnection { id: string; client_name: string; scopes: string[]; expires_at: string; resource?: string }

function JsonBlock({ json, label, hint }: { json: string; label: string; hint: string }) {
  return <div className="space-y-1">
    <div className="flex items-center justify-between gap-2"><span className="text-xs font-medium text-foreground">{label}</span><CopyButton text={json} label={label} /></div>
    <pre className="overflow-x-auto rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground" aria-label={label}>{json}</pre>
    <p className="text-xs text-muted-foreground">{hint}</p>
  </div>
}

/** One connection path at a time. Browser approval keeps credentials out of setup instructions. */
export function CliMcpSetupContent({ target = 'agentworks', endpoint, projectName, connectionName }: { target?: 'agentworks' | 'vault'; endpoint?: string; projectName?: string; connectionName?: string } = {}) {
  const localAccount = useAuthStore(state => state.isMultiUserModeChecked && !state.isMultiUserMode)
  const serverName = connectionName || target
  const serviceName = target === 'vault' ? 'Vault' : 'AgentWorks'
  const connectionPath = `/api/oauth/${target === 'vault' ? 'vault' : 'mcp'}/connections`
  const [app, setApp] = useState<AiApp | null>(null)
  const [destination, setDestination] = useState<Destination>('local-assistant')
  const [localClient, setLocalClient] = useState<LocalClient>('claude-code')
  const [hostedClient, setHostedClient] = useState<HostedClient>('chatgpt')
  const [connections, setConnections] = useState<OAuthConnection[]>([])
  const [connectionLoadError, setConnectionLoadError] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [skillBusy, setSkillBusy] = useState<'download' | 'copy' | 'plugin' | null>(null)
  const [skillMsg, setSkillMsg] = useState<string | null>(null)
  const [pluginMsg, setPluginMsg] = useState<string | null>(null)
  const origin = (getApiBaseUrl() || window.location.origin).replace(/\/+$/, '')
  const quoted = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`
  const mcpUrl = endpoint ?? `${origin}/api/external/v1/mcp`
  const mcpJson = JSON.stringify({ mcpServers: { [serverName]: { url: mcpUrl } } }, null, 2)
  const museJson = JSON.stringify({ schema_version: 1, mcpServers: { [serverName]: { url: mcpUrl } } }, null, 2)
  const isLoopbackOrigin = (() => { try { return ['localhost', '127.0.0.1', '0.0.0.0', '::1'].includes(new URL(mcpUrl).hostname.replace(/^\[|\]$/g, '')) } catch { return false } })()

  const useLocalToken = target === 'agentworks' && localAccount && isLoopbackOrigin && !projectName

  useEffect(() => {
    if (useLocalToken) return
    let cancelled = false
    api.get<{ connections: OAuthConnection[] }>(connectionPath)
      .then(({ data }) => {
        if (cancelled) return
        if (Array.isArray(data?.connections)) {
          setConnections(projectName ? data.connections.filter(item => item.resource === mcpUrl || item.resource?.replace(/\/(view|use)$/, '') === mcpUrl.replace(/\/(view|use)$/, '')) : data.connections)
          return
        }
        setConnections([])
        setConnectionLoadError(true)
      })
      .catch(() => { /* Setup instructions remain available. */ })
    return () => { cancelled = true }
  }, [connectionPath, target, useLocalToken, projectName, mcpUrl])

  const revoke = async (id: string) => {
    setError(null)
    try {
      await api.delete(`${connectionPath}/${encodeURIComponent(id)}`)
      setConnections(current => current.filter(item => item.id !== id))
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not revoke connection') }
  }

  const downloadSkill = async () => {
    setSkillBusy('download'); setSkillMsg(null)
    try {
      const blob = await externalSkillApi.downloadSkillZIP()
      const url = URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = url; anchor.download = 'agentworks-skill.zip'
        document.body.appendChild(anchor); anchor.click(); anchor.remove()
      } finally { URL.revokeObjectURL(url) }
      setSkillMsg('Downloaded agentworks-skill.zip')
    } catch { setSkillMsg('Download failed — try again.') }
    finally { setSkillBusy(null) }
  }

  const downloadCoworkPlugin = async () => {
    setSkillBusy('plugin'); setPluginMsg(null)
    try {
      const blob = await externalSkillApi.downloadCoworkPlugin()
      const url = URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = url; anchor.download = 'agentworks.plugin'
        document.body.appendChild(anchor); anchor.click(); anchor.remove()
      } finally { URL.revokeObjectURL(url) }
      setPluginMsg('Downloaded agentworks.plugin')
    } catch { setPluginMsg('Plugin download failed — try again.') }
    finally { setSkillBusy(null) }
  }

  const copySkill = async () => {
    setSkillBusy('copy'); setSkillMsg(null)
    try { await navigator.clipboard.writeText(await externalSkillApi.fetchSkillMD()); setSkillMsg('Skill text copied') }
    catch { setSkillMsg('Copy failed — try again.') }
    finally { setSkillBusy(null) }
  }

  if (useLocalToken) return <LocalMcpTokenPanel endpoint={mcpUrl} origin={origin} />

  return <div className="space-y-5">
    {!projectName && <div><h3 className="text-base font-semibold text-foreground">Use {serviceName} in your AI app</h3><p className="mt-1 text-sm text-muted-foreground">{target === 'vault' ? 'Let your AI app use the tools and secrets allowed by your Vault access.' : projectName ? `Connect your AI app to ${projectName}. Access is limited to this project.` : 'Let your AI app work with the projects, knowledge and tools you can access.'}</p></div>}
    <AiAppPicker value={app} localOnly={isLoopbackOrigin} onChange={next => { setApp(next); if (isHostedAiApp(next)) { setDestination('hosted-assistant'); setHostedClient(next) } else if (next) { setDestination('local-assistant'); setLocalClient(next) } }} />
    {app && <>
    {destination === 'hosted-assistant' ? <HostedAiAppGuide app={hostedClient} endpoint={mcpUrl} serviceName={serviceName} localServer={isLoopbackOrigin}>
      {hostedClient === 'cowork' && target === 'agentworks' && !projectName && <details className="rounded-md border border-border p-3 text-xs text-muted-foreground"><summary className="cursor-pointer font-medium">Optional: Cowork plugin</summary><p className="my-2">Download the plugin, then open Customize → Plugins in Cowork and upload it.</p><Button variant="outline" size="sm" disabled={skillBusy !== null || isLoopbackOrigin} onClick={() => void downloadCoworkPlugin()}><Download className="mr-1 h-3.5 w-3.5" />Download Cowork plugin</Button>{pluginMsg && <p className="mt-2">{pluginMsg}</p>}</details>}
      {target === 'agentworks' && !projectName && hostedClient === 'chatgpt' && <details className="rounded-md border border-border p-3 text-xs text-muted-foreground"><summary className="cursor-pointer font-medium">Optional: give the assistant workflow guidance</summary><p className="mt-2">Upload the skill where Skills are supported, or paste its text into the app's custom instructions.</p><div className="mt-3 flex flex-wrap gap-2"><Button variant="outline" size="sm" disabled={skillBusy !== null} onClick={() => void downloadSkill()}><Download className="mr-1 h-3.5 w-3.5" />Download skill .zip</Button><Button variant="ghost" size="sm" disabled={skillBusy !== null} onClick={() => void copySkill()}><Copy className="mr-1 h-3.5 w-3.5" />Copy skill text</Button></div>{skillMsg && <p className="mt-2">{skillMsg}</p>}</details>}
    </HostedAiAppGuide> : <SettingsCard icon={<Plug className="h-4 w-4 text-primary" />} title={`Connect ${AI_APPS.find(item => item.id === app)?.name}`} description={`Follow these steps, then approve access when ${serviceName} opens in your browser.`}>
      <div className="space-y-4">
        <div className="space-y-2">
          {localClient === 'claude-code' ? <CommandRow label={`Add ${serviceName} to Claude Code`} command={`claude mcp add --transport http ${serverName} ${quoted(mcpUrl)}`} />
            : localClient === 'codex' ? <div className="space-y-2"><CommandRow label={`Add ${serviceName} to Codex`} command={`codex mcp add ${serverName} --url ${quoted(mcpUrl)}`} /><CommandRow label={`Sign in to ${serviceName}`} command={`codex mcp login ${serverName}`} /></div>
              : localClient === 'cursor' ? <div className="space-y-2"><JsonBlock label="Add to ~/.cursor/mcp.json" json={mcpJson} hint={`Merge the ${serverName} entry into mcpServers if the file already has servers.`} /><CommandRow label={`Sign in to ${serviceName}`} command={`cursor-agent mcp login ${serverName}`} /></div>
              : localClient === 'muse' ? <div className="space-y-2"><JsonBlock label="Add to ~/.config/muse/settings.json" json={museJson} hint={`Merge the ${serverName} entry into mcpServers if the file already has settings.`} /><CommandRow label={`Sign in to ${serviceName}`} command={`muse mcp login ${serverName}`} /></div>
              : <JsonBlock label="MCP client config" json={mcpJson} hint="Use the URL with your client's HTTP MCP transport and OAuth sign-in." />}
        </div>
        <p className="text-xs text-muted-foreground">Approve the OAuth connection in your browser when your agent asks. A local server URL works only for agents on this computer.</p>
        {target === 'agentworks' && !projectName && localClient === 'claude-code' && <details className="rounded-md border border-border p-3 text-xs text-muted-foreground"><summary className="cursor-pointer font-medium text-foreground">Optional: use the AgentWorks skill</summary><p className="mt-2">The skill teaches Claude Code how to use the workflow tools. Once {serviceName} is connected, ask Claude to install it; it fetches the text through its own connection, so nothing is public and nothing is pasted.</p><div className="mt-2"><CommandRow label="Tell Claude" command="Install the AgentWorks skill: call get_skill and save its content to ~/.claude/skills/agentworks/SKILL.md" /></div><div className="mt-3 flex flex-wrap gap-2"><Button variant="ghost" size="sm" disabled={skillBusy !== null} onClick={() => void copySkill()}><Copy className="mr-1 h-3.5 w-3.5" />Copy skill text</Button></div>{skillMsg && <p className="mt-2">{skillMsg}</p>}</details>}
      </div>
    </SettingsCard>}
    </>}
    {connectionLoadError && <p role="alert" className="text-sm text-amber-600">Connected apps are unavailable. Restart or update the AgentWorks server to use browser sign-in.</p>}
    {connections.length > 0 && <SettingsCard icon={<Plug className="h-4 w-4 text-primary" />} title="Connected apps" description="Revoke access to an AI app when you are done.">
      <div className="space-y-2">{connections.map(item => <div key={item.id} className="flex items-center justify-between gap-2 text-sm"><span className="min-w-0 truncate">{item.client_name}</span><Button variant="outline" size="sm" onClick={() => void revoke(item.id)}>Revoke</Button></div>)}</div>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </SettingsCard>}
  </div>
}
