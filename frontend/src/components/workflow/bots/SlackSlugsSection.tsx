import { useState } from 'react'
import { Hash, Loader2, Plus, Trash2 } from 'lucide-react'
import { Button } from '../../ui/Button'
import { Input } from '../../ui/Input'
import { ToggleRow } from '../../ui/ToggleRow'
import { agentApi } from '../../../services/api'
import type { SlackTargetSettings } from '../../../services/api-types'

// The platform ("AgentWorks") bot for one target (docs/design/slack_slugs.md,
// PLAT-668): its switch and the platform channels it answers in. Rendered
// only when the server has a platform bot; a server with own bots only never
// shows it (server A 2026-10-07).

const CHANNEL_RE = /^[CG][A-Z0-9]{2,}$/

export type SlackDestination = { workspace_path: string; profile_id?: string }

export const slackErrorText = (err: unknown, fallback: string): string => {
  const data = (err as { response?: { data?: unknown } })?.response?.data
  if (typeof data === 'string' && data.trim()) return data.trim()
  return err instanceof Error ? err.message : fallback
}

export function SlackSlugsSection({ destination, noun, readOnly, dmOnly = false, settings, onSettings }: {
  destination: SlackDestination
  noun: string
  readOnly: boolean
  /** A Code: 1:1 DMs only, never channels. */
  dmOnly?: boolean
  settings: SlackTargetSettings
  onSettings: (next: SlackTargetSettings) => void
}) {
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [channel, setChannel] = useState('')
  const [tests, setTests] = useState<Record<string, string>>({})

  if (!settings.platform_available) return null

  const run = async (key: string, action: () => Promise<SlackTargetSettings>) => {
    setBusy(key)
    setError(null)
    try {
      onSettings(await action())
    } catch (err) {
      setError(slackErrorText(err, 'The change was not saved'))
    } finally {
      setBusy(null)
    }
  }
  const test = async (channelId: string) => {
    setTests(prev => ({ ...prev, [channelId]: 'Testing…' }))
    try {
      const outcome = await agentApi.dryRunSlackTarget(channelId, destination)
      setTests(prev => ({ ...prev, [channelId]: outcome.admitted
        ? `Answers: ${outcome.destination || 'ok'}`
        : `Would not answer: ${outcome.replies?.[0] || outcome.reason || 'unknown'}` }))
    } catch (err) {
      setTests(prev => ({ ...prev, [channelId]: slackErrorText(err, 'Test failed') }))
    }
  }

  const canManage = settings.can_manage && !readOnly
  const title = canManage ? undefined : `Only the ${noun}'s owner can change this`
  const botName = settings.platform_name || 'the platform bot'

  return (
    <div className="space-y-3">
      <ToggleRow
        label={`Answer through @${botName}`}
        description={settings.platform_bot
          ? 'On. Turning it off cuts access at once, including open threads.'
          : `Off. Nothing reaches this ${noun} through @${botName}.`}
        checked={settings.platform_bot}
        onCheckedChange={checked => void run('switch', () => agentApi.updateSlackTargetSettings(destination, { platform_bot: checked }))}
        disabled={!canManage || !!busy || (!settings.platform_bot && !settings.product_allowed)}
        disabledTitle={title}
      />
      {!settings.product_allowed && <p className="text-xs text-amber-700 dark:text-amber-300">An admin has not allowed this product to use @{botName}.</p>}

      {!dmOnly && settings.platform_bot && (
        <div className="space-y-2">
          {settings.channels.map(card => (
            <div key={card.channel_id} className="flex min-w-0 flex-wrap items-center gap-2 rounded-md border border-border bg-background p-2 text-sm">
              <Hash className="h-4 w-4 shrink-0 text-muted-foreground" aria-label="Slack channel" />
              <span className="font-mono font-semibold text-foreground">{card.channel_id}</span>
              <span className="flex min-w-0 flex-1 flex-wrap gap-1">
                {card.targets.map(target => (
                  <span key={`${target.profile_id || ''}|${target.workspace_path}`} title={target.label} className={`rounded px-1.5 py-0.5 font-mono text-[11px] ${target.is_this ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground'}`}>
                    {target.slug}
                  </span>
                ))}
              </span>
              <Button type="button" variant="ghost" size="xs" onClick={() => void test(card.channel_id)} disabled={!canManage}>Test</Button>
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={() => void run(`remove:${card.channel_id}`, () => agentApi.removeSlackTargetChannel(card.channel_id, destination))}
                disabled={!canManage || !!busy}
                aria-label={`Stop answering in ${card.channel_id}`}
                title={title}
              >
                {busy === `remove:${card.channel_id}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
              </Button>
              {tests[card.channel_id] && <span className="basis-full text-[11px] text-muted-foreground">{tests[card.channel_id]}</span>}
            </div>
          ))}
          <div className="flex flex-wrap items-center gap-2">
            <Input
              aria-label={`Channel ID for @${botName}`}
              value={channel}
              onChange={e => setChannel(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))}
              placeholder="channel ID, e.g. C1234567890"
              disabled={!canManage || !!busy}
              className="h-8 min-w-0 flex-1 font-mono text-xs"
            />
            <Button
              variant="outline"
              size="sm"
              onClick={() => void run('add', async () => {
                const next = await agentApi.addSlackTargetChannel(channel, destination, false)
                setChannel('')
                return next
              })}
              disabled={!canManage || !!busy || !CHANNEL_RE.test(channel)}
            >
              {busy === 'add' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
              Add channel
            </Button>
          </div>
        </div>
      )}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
    </div>
  )
}
