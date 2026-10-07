import { useCallback, useEffect, useState } from 'react'
import { Hash, Loader2, Plus, Trash2 } from 'lucide-react'
import { Button } from '../../ui/Button'
import { FormSection } from '../../ui/FormSection'
import { Input } from '../../ui/Input'
import { ToggleRow } from '../../ui/ToggleRow'
import { agentApi } from '../../../services/api'
import type { SlackTargetSettings, SlackUsableBot } from '../../../services/api-types'
import { sameBotWorkspacePath } from './slackWorkflowConnection'

// Slack slugs (docs/design/slack_slugs.md, PLAT-668): one Slack app answers
// for many workflows, Crews and Codes. This section is a target's switch for
// the AgentWorks (platform) bot, its slug, and the channels it answers in.
// A Code answers 1:1 DMs only: no channels, and it can also be reached through
// one of its owner's bots by its slug.

const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/
const CHANNEL_RE = /^[CG][A-Z0-9]{2,}$/

type Destination = { workspace_path: string; profile_id?: string }

const errorText = (err: unknown, fallback: string): string => {
  const data = (err as { response?: { data?: unknown } })?.response?.data
  if (typeof data === 'string' && data.trim()) return data.trim()
  return err instanceof Error ? err.message : fallback
}

export function SlackSlugsSection({ destination, noun, readOnly, dmOnly = false, myOtherBots = [], onBotsChanged }: {
  destination: Destination
  noun: string
  readOnly: boolean
  /** A Code: 1:1 DMs only, never channels. */
  dmOnly?: boolean
  /** Bots this user manages, for attaching a Code's DM slug to one of them. */
  myOtherBots?: SlackUsableBot[]
  onBotsChanged?: () => void
}) {
  const [settings, setSettings] = useState<SlackTargetSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [slug, setSlug] = useState('')
  const [channel, setChannel] = useState('')
  const [makeDefault, setMakeDefault] = useState(false)
  const [botId, setBotId] = useState('')
  const [tests, setTests] = useState<Record<string, string>>({})

  const test = async (channelId: string) => {
    setTests(prev => ({ ...prev, [channelId]: 'Testing…' }))
    try {
      const outcome = await agentApi.dryRunSlackTarget(channelId, destination)
      setTests(prev => ({ ...prev, [channelId]: outcome.admitted
        ? `Answers: ${outcome.destination || 'ok'}${outcome.mode === 'run' ? ' · Run mode' : ''}`
        : `Would not answer: ${outcome.replies?.[0] || outcome.reason || 'unknown'}` }))
    } catch (err) {
      setTests(prev => ({ ...prev, [channelId]: errorText(err, 'Test failed') }))
    }
  }

  const destinationKey = `${destination.profile_id || ''}|${destination.workspace_path}`
  const load = useCallback(async () => {
    if (!destination.workspace_path) return
    setLoading(true)
    try {
      const next = await agentApi.getSlackTargetSettings(destination)
      setSettings(next)
      setSlug(next.slug)
      setError(null)
    } catch (err) {
      setError(errorText(err, 'Could not load the Slack settings'))
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [destinationKey])
  useEffect(() => { void load() }, [load])

  const run = async (key: string, action: () => Promise<SlackTargetSettings | void>) => {
    setBusy(key)
    setError(null)
    try {
      const next = await action()
      if (next) {
        setSettings(next)
        setSlug(next.slug)
      }
    } catch (err) {
      setError(errorText(err, 'The change was not saved'))
    } finally {
      setBusy(null)
    }
  }

  if (loading && !settings) {
    return <div className="flex items-center justify-center py-6"><Loader2 className="h-5 w-5 animate-spin text-primary" /></div>
  }
  if (!settings) {
    return error ? <p className="text-xs text-red-600 dark:text-red-400">{error}</p> : null
  }

  const canManage = settings.can_manage && !readOnly
  const title = canManage ? undefined : `Only the ${noun}'s owner can change this`
  const slugValid = SLUG_RE.test(slug) && slug !== 'list'
  const botName = settings.platform_name || 'AgentWorks'
  const attachedTo = myOtherBots.filter(bot => (bot.targets || []).some(target =>
    sameBotWorkspacePath(target.workspace_path, destination.workspace_path) && (target.profile_id || '') === (destination.profile_id || '')))
  const attachable = myOtherBots.filter(bot => !attachedTo.includes(bot))
  const pickedBot = attachable.find(bot => bot.id === botId) || attachable[0]

  return (
    <FormSection
      title="Use the AgentWorks bot"
      description={dmOnly
        ? <>One shared Slack bot, no app of your own. People DM <b>@{botName}</b> and start with <code className="rounded bg-muted px-1 font-mono">{settings.slug || 'slug'}</code>. A Code answers its owner only, in DMs, never in channels.</>
        : <>One shared Slack bot, no app of your own. In a channel: <code className="rounded bg-muted px-1 font-mono">@{botName} {settings.slug || 'slug'} your question</code> (the thread then stays with this {noun}). In a DM: start with <code className="rounded bg-muted px-1 font-mono">{settings.slug || 'slug'}</code>. People reach it with their own access.</>}
    >
      {!settings.platform_available && !settings.platform_bot && (
        <p className="text-xs text-muted-foreground">An admin has not set up the AgentWorks bot yet (Access → Slack).</p>
      )}
      {!settings.product_allowed && (
        <p className="text-xs text-amber-700 dark:text-amber-300">An admin has not allowed this product to use the AgentWorks bot.</p>
      )}
      <ToggleRow
        label="Use the AgentWorks bot"
        description={settings.platform_bot ? 'On: reachable from Slack. Turning it off cuts access at once, including open threads.' : 'Off: nothing reaches this from the AgentWorks bot.'}
        checked={settings.platform_bot}
        onCheckedChange={checked => void run('switch', () => agentApi.updateSlackTargetSettings(destination, { platform_bot: checked }))}
        disabled={!canManage || !!busy || (!settings.platform_bot && !settings.product_allowed)}
        disabledTitle={title}
      />
      <div className="flex items-center gap-2">
        <span className="text-xs text-muted-foreground">Slug</span>
        <Input
          aria-label="Slack slug"
          value={slug}
          onChange={e => setSlug(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))}
          disabled={!canManage || !!busy}
          title={title}
          className="h-8 max-w-[14rem] font-mono text-xs"
        />
        <Button
          variant="outline"
          size="sm"
          onClick={() => void run('slug', () => agentApi.updateSlackTargetSettings(destination, { slug }))}
          disabled={!canManage || !!busy || !slugValid || slug === settings.slug}
          title={!slugValid ? 'Lowercase letters, digits and dashes' : title}
        >
          {busy === 'slug' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : 'Save slug'}
        </Button>
      </div>

      {!dmOnly && settings.platform_bot && (
        <div className="space-y-2">
          <h4 className="text-xs font-medium text-muted-foreground">Channels</h4>
          {settings.channels.length === 0 && <p className="text-xs text-muted-foreground">No channels yet. Invite the bot to a channel you are in, then add its ID (starts with C, under View channel details).</p>}
          {settings.channels.map(card => (
            <div key={card.channel_id} className="flex min-w-0 flex-wrap items-center gap-2 rounded-md border border-border bg-background p-2 text-sm shadow-sm">
              <Hash className="h-4 w-4 shrink-0 text-muted-foreground" aria-label="Slack channel" />
              <span className="font-mono font-semibold text-foreground">{card.channel_id}</span>
              <span className="flex min-w-0 flex-1 flex-wrap gap-1">
                {card.targets.map(target => (
                  <span
                    key={`${target.profile_id || ''}|${target.workspace_path}`}
                    title={target.label}
                    className={`rounded px-1.5 py-0.5 font-mono text-[11px] ${target.is_this ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground'}`}
                  >
                    {target.slug}{target.is_default ? ' · default' : ''}
                  </span>
                ))}
              </span>
              {card.admin_route && <span className="text-[11px] text-muted-foreground">admin route</span>}
              <Button type="button" variant="ghost" size="xs" onClick={() => void test(card.channel_id)} disabled={!canManage} title={title || `Try @bot ${settings.slug} in ${card.channel_id} without posting`}>
                Test
              </Button>
              {tests[card.channel_id] && <span className="basis-full text-[11px] text-muted-foreground">{tests[card.channel_id]}</span>}
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={() => void run(`remove:${card.channel_id}`, () => agentApi.removeSlackTargetChannel(card.channel_id, destination))}
                disabled={!canManage || !!busy}
                className="shrink-0 px-1 text-muted-foreground hover:bg-red-500/10 hover:text-red-600"
                aria-label={`Stop answering in ${card.channel_id}`}
                title={title || `Stop answering in ${card.channel_id}`}
              >
                {busy === `remove:${card.channel_id}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
              </Button>
            </div>
          ))}
          <div className="flex flex-wrap items-center gap-2">
            <Input
              aria-label="Channel ID for the AgentWorks bot"
              value={channel}
              onChange={e => setChannel(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))}
              placeholder="channel ID, e.g. C1234567890"
              disabled={!canManage || !!busy}
              title={title}
              className="h-8 min-w-0 flex-1 font-mono text-xs"
            />
            <label className="flex items-center gap-1 text-xs text-muted-foreground">
              <input type="checkbox" checked={makeDefault} onChange={e => setMakeDefault(e.target.checked)} disabled={!canManage || !!busy} />
              Default (answers without a slug)
            </label>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void run('add', async () => {
                const next = await agentApi.addSlackTargetChannel(channel, destination, makeDefault)
                setChannel('')
                return next
              })}
              disabled={!canManage || !!busy || !CHANNEL_RE.test(channel)}
              title={title}
            >
              {busy === 'add' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
              Add channel
            </Button>
          </div>
          <p className="text-[11px] text-muted-foreground">You can add only a channel you are a member of. With several targets and no default, the bot asks with buttons.</p>
        </div>
      )}

      {dmOnly && (
        <div className="space-y-2 border-t border-border pt-2">
          <h4 className="text-xs font-medium text-muted-foreground">Or through one of my bots</h4>
          {attachedTo.map(bot => (
            <div key={bot.id} className="flex items-center gap-2 text-sm">
              <span className="min-w-0 flex-1 truncate">{bot.display_name}{bot.owner_label ? ` (${bot.owner_label})` : ''}</span>
              <Button
                variant="ghost"
                size="xs"
                onClick={() => void run(`detach:${bot.id}`, async () => { await agentApi.detachSlackBotTarget(bot.id, destination); onBotsChanged?.() })}
                disabled={readOnly || !!busy}
                aria-label={`Stop answering DMs through ${bot.display_name}`}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
          {attachable.length > 0 ? (
            <div className="flex items-center gap-2">
              <select
                aria-label="One of my bots"
                value={pickedBot?.id || ''}
                onChange={e => setBotId(e.target.value)}
                className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm"
                disabled={readOnly || !!busy}
              >
                {attachable.map(bot => <option key={bot.id} value={bot.id}>{bot.display_name}{bot.owner_label ? ` (${bot.owner_label})` : ''}</option>)}
              </select>
              <Button
                variant="outline"
                size="sm"
                onClick={() => pickedBot && void run(`attach:${pickedBot.id}`, async () => { await agentApi.attachSlackBotTarget(pickedBot.id, destination); onBotsChanged?.() })}
                disabled={readOnly || !!busy || !pickedBot}
              >
                <Plus className="h-3.5 w-3.5" />
                Answer DMs here
              </Button>
            </div>
          ) : attachedTo.length === 0 && (
            <p className="text-xs text-muted-foreground">You have no other Slack bots. Give a workflow or Crew its own bot first, or use the AgentWorks bot above.</p>
          )}
          <p className="text-[11px] text-muted-foreground">DM that bot and start with <code className="rounded bg-muted px-1 font-mono">{settings.slug}</code>. Only you can reach this Code.</p>
        </div>
      )}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
    </FormSection>
  )
}
