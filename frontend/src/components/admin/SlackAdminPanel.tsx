import { useCallback, useEffect, useState } from 'react'
import { CheckCircle, Loader2, MessageSquare, Users } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { SlackConfig, SlackTestResponse } from '../../services/api-types'
import { SettingsCard, SettingsEmpty } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import { SecretField } from '../ui/SecretField'
import { ToggleRow } from '../ui/ToggleRow'
import { StatusBanner } from '../workflow/bots/StatusBanner'
import { SlackAppSetupSteps, SlackChecksView, SlackManifestSetup, SlackPermissionsChecklist, SlackTokenHint } from '../workflow/bots/SlackAppSetupSteps'

/**
 * Access → Slack (admins): the platform side of Slack. The shared bot is the
 * default app every workflow without its own bot answers through, in the
 * channels routed to it. Workflows and crew projects set up their own bots
 * and shared-bot channels from their own Slack tab.
 */
export default function SlackAdminPanel() {
  return <SlackAdminSections showWorkflowBots />
}

/** The shared bot's settings alone, for a workflow's Slack tab. */
export function SharedSlackBotSettings({ onSaved }: { onSaved?: () => void }) {
  return <SlackAdminSections onSaved={onSaved} />
}

function SlackAdminSections({ showWorkflowBots = false, onSaved }: { showWorkflowBots?: boolean; onSaved?: () => void }) {
  const [original, setOriginal] = useState<SlackConfig | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [botToken, setBotToken] = useState('')
  const [appToken, setAppToken] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<SlackTestResponse | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await agentApi.getSlackFeedbackConfig()
      setOriginal(data)
      setEnabled(!!data.enabled && !!data.bot_mode)
      setBotToken(data.bot_token || '')
      setAppToken(data.app_token || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load Slack settings')
    } finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => { void load() }, [load])

  const canManage = original?.manage_default_allowed === true
  const dirty = !!original && (
    enabled !== (!!original.enabled && !!original.bot_mode)
    || botToken !== (original.bot_token || '')
    || appToken !== (original.app_token || '')
  )

  const save = async (): Promise<boolean> => {
    if (!original) return false
    setSaving(true)
    setError(null)
    setSuccess(null)
    try {
      // Channel routes belong to workflows; carry them through unchanged.
      await agentApi.updateSlackFeedbackConfig({
        enabled, bot_mode: enabled, bot_token: botToken, app_token: appToken,
        channel_routing: original.channel_routing || {},
      })
      setTestResult(null)
      setSuccess('Shared bot saved.')
      await load()
      onSaved?.()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save Slack settings')
      return false
    } finally {
      setSaving(false)
    }
  }

  const test = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      if (dirty && !await save()) return
      setTestResult(await agentApi.testSlackConnection())
    } catch (err) {
      setTestResult({ success: false, message: err instanceof Error ? err.message : 'Connection test failed' })
    } finally {
      setTesting(false)
    }
  }

  if (loading && !original) {
    return <div className="flex justify-center py-12"><Loader2 className="h-6 w-6 animate-spin text-muted-foreground" /></div>
  }

  const workflowBots = (original?.connections || []).filter(conn => !conn.is_default && conn.workspace_path)
  const disabledTitle = canManage ? undefined : 'Only a platform admin can change the shared bot'

  return (
    <div className="space-y-4">
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      {success && <StatusBanner tone="success">{success}</StatusBanner>}

      <SettingsCard
        icon={<Users className="h-4 w-4 text-muted-foreground" />}
        title="Shared bot"
        description="One Slack app for many workflows. Each workflow or crew project picks the channels it answers in from its own Slack tab. Workflows with their own bot don't use it."
      >
        <ToggleRow
          label="Shared bot enabled"
          description="Answers @mentions in every channel the shared bot is in, not only routed ones. Routed channels, DMs to targets whose owners turned on “Use the AgentWorks bot”, and own bots keep working when this is off."
          checked={enabled}
          onCheckedChange={setEnabled}
          disabled={!canManage}
          disabledTitle={disabledTitle}
        />
        <SlackManifestSetup finalStep={<>Save. Owners then invite the bot to their channels.</>} />
        <SlackAppSetupSteps finalStep={<>Save below. Owners then invite the bot to their channels and add those channels in their workflow's Slack tab.</>} />
        <SlackPermissionsChecklist />
        <SecretField label="Bot Token" hint={<SlackTokenHint kind="bot" />} value={botToken} onChange={setBotToken} disabled={!canManage} placeholder="xoxb-..." disabledTitle={disabledTitle} />
        <SecretField label="App Token (Socket Mode)" hint={<SlackTokenHint kind="app" />} value={appToken} onChange={setAppToken} disabled={!canManage} placeholder="xapp-..." disabledTitle={disabledTitle} />
        <div className="flex items-center gap-2">
          <Button onClick={() => void save()} disabled={!canManage || !dirty || saving || testing} title={disabledTitle} className="flex items-center gap-2">
            {saving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving...</> : <><CheckCircle className="h-4 w-4" />Save</>}
          </Button>
          <Button variant="outline" onClick={() => void test()} disabled={!canManage || !enabled || saving || testing} title={disabledTitle}>
            {testing ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Testing...</> : 'Save & test'}
          </Button>
        </div>
        {testResult && <SlackChecksView result={testResult} />}
      </SettingsCard>

      {showWorkflowBots && <PlatformBotProducts canManage={canManage} />}

      {showWorkflowBots && <SettingsCard
        icon={<MessageSquare className="h-4 w-4 text-muted-foreground" />}
        title="Workflow and crew bots"
        count={workflowBots.length}
        description="Bots that answer only for their own workflow or crew project, in any channel they're invited to. Owners manage them from that workflow's Slack tab."
      >
        {workflowBots.length === 0 ? (
          <SettingsEmpty>No workflow has its own bot yet.</SettingsEmpty>
        ) : (
          <ul className="divide-y divide-border">
            {workflowBots.map(conn => (
              <li key={conn.id} className="flex items-center gap-2 py-2">
                <span className="font-medium text-foreground">{conn.display_name}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={conn.workspace_path}>{conn.workspace_path}</span>
                <span className={conn.enabled && conn.configured ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}>
                  {!conn.configured ? 'Missing tokens' : conn.enabled ? 'Ready' : 'Disabled'}
                </span>
              </li>
            ))}
          </ul>
        )}
      </SettingsCard>}
    </div>
  )
}

const PLATFORM_PRODUCTS: { id: string; label: string }[] = [
  { id: 'workflows', label: 'Workflows' },
  { id: 'crew', label: 'Crews' },
  { id: 'code', label: 'Codes (DMs only)' },
]

/**
 * Which products may use the AgentWorks (shared) bot (PLAT-668). Nothing is
 * reachable until a target's owner turns on "Use the AgentWorks bot" in its
 * own Slack tab; this limits who may turn it on. None ticked = all.
 */
function PlatformBotProducts({ canManage }: { canManage: boolean }) {
  const [products, setProducts] = useState<string[] | null>(null)
  const [targetsOn, setTargetsOn] = useState(0)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (!canManage) return
    agentApi.getSlackPlatformBotSettings()
      .then(settings => { setProducts(settings.products || []); setTargetsOn(settings.targets_on || 0) })
      .catch(err => setError(err instanceof Error ? err.message : 'Failed to load'))
  }, [canManage])
  if (!canManage || products === null) return error ? <StatusBanner tone="error">{error}</StatusBanner> : null
  const all = products.length === 0
  const toggle = async (id: string, checked: boolean) => {
    const current = all ? PLATFORM_PRODUCTS.map(product => product.id) : products
    const next = checked ? Array.from(new Set([...current, id])) : current.filter(product => product !== id)
    setSaving(true)
    setError(null)
    try {
      const saved = await agentApi.updateSlackPlatformBotSettings(next.length === PLATFORM_PRODUCTS.length ? [] : next)
      setProducts(saved.products || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save')
    } finally {
      setSaving(false)
    }
  }
  return (
    <SettingsCard
      icon={<Users className="h-4 w-4 text-muted-foreground" />}
      title="Who may use the AgentWorks bot"
      count={targetsOn}
      description="Owners turn on “Use the AgentWorks bot” in a workflow's, Crew's or Code's Slack tab; people then reach it with @bot <slug> in its channels or by slug in a DM, with their own access."
    >
      {PLATFORM_PRODUCTS.map(product => (
        <ToggleRow
          key={product.id}
          label={product.label}
          checked={all || products.includes(product.id)}
          onCheckedChange={checked => void toggle(product.id, checked)}
          disabled={saving}
        />
      ))}
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
    </SettingsCard>
  )
}
