import { useEffect, useState, type ReactNode } from 'react'
import { AlertTriangle, Bot, Check, CheckCircle, Copy, Loader2, MessageSquare, Plus, Trash2, Users } from 'lucide-react'
import { Button } from '../../ui/Button'
import { FormSection } from '../../ui/FormSection'
import { Input } from '../../ui/Input'
import { Label } from '../../ui/label'
import { SecretField } from '../../ui/SecretField'
import { ToggleRow } from '../../ui/ToggleRow'
import { READ_ONLY_TITLE } from '../../../hooks/useCanWriteWorkflow'
import type { SlackConnection, SlackTargetSettings, SlackUsableBot } from '../../../services/api-types'
import { agentApi } from '../../../services/api'
import { SlackBotWhere } from './SlackBotWhere'
import { sameBotWorkspacePath } from './slackWorkflowConnection'
import type { WorkflowBots } from './useWorkflowBots'
import { StatusBanner } from './StatusBanner'
import { SharedSlackBotSettings } from '../../admin/SlackAdminPanel'
import { RouteChip } from './RouteChips'
import { SlackSlugsSection, slackErrorText } from './SlackSlugsSection'
import { SlackAppSetupSteps, SlackChecksView, SlackManifestSetup, SlackPermissionsChecklist, SlackTokenHint } from './SlackAppSetupSteps'
import { routeId } from './types'

// The Slack tab for one workflow, Crew or Code is three blocks (owner,
// 2026-10-07: "ui is not clear"):
//
//  1. Which bot answers: its own bot, one of my bots (another target's own
//     bot this user manages), or the platform bot when the server has one.
//  2. How people reach it: "@Bot <slug> your question" in a channel, and
//     "<slug> your question" in a DM, with the slug editable here.
//  3. Where it answers: Slack decides. A bot answers in every channel it is
//     in and in DMs; with several targets on one bot the slug (or buttons)
//     picks one. One read-only line, no channel setup here.
//
// The bot's own settings stay visible on its own target.

const OWNER_ONLY_TITLE = 'Only an owner can manage this Slack bot'

type SlackMode = 'own' | 'mine' | 'shared'

function connStatus(conn: SlackConnection): { label: string; ok: boolean } {
  if (!conn.configured) return { label: 'Missing tokens', ok: false }
  if (!conn.enabled) return { label: 'Disabled', ok: false }
  return { label: 'Ready', ok: true }
}

function StatusDot({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className={`inline-flex items-center gap-1 text-[11px] font-medium ${ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : 'bg-amber-500'}`} />
      {label}
    </span>
  )
}

function ModeOption({ checked, disabled, title, onSelect, icon, label, hint }: {
  checked: boolean; disabled: boolean; title?: string; onSelect: () => void; icon: ReactNode; label: string; hint: string
}) {
  return (
    <label
      title={title}
      className={`flex flex-1 items-start gap-2 rounded-md border p-3 text-sm transition-colors ${checked ? 'border-primary bg-primary/5' : 'border-border'} ${disabled ? 'opacity-60' : 'cursor-pointer hover:bg-muted/40'}`}
    >
      <input type="radio" name="slack-mode" className="mt-1" checked={checked} disabled={disabled} onChange={onSelect} />
      <span className="min-w-0">
        <span className="flex items-center gap-1.5 font-medium text-foreground">{icon}{label}</span>
        <span className="mt-0.5 block text-xs text-muted-foreground">{hint}</span>
      </span>
    </label>
  )
}

type SlackSetupBots = Pick<WorkflowBots,
  | 'readOnly' | 'workflowId' | 'slackAppDefaultName'
  | 'slackOriginal' | 'loadSlack' | 'canManageSlackDefault' | 'slackLoading' | 'slackError' | 'slackSuccess'
  | 'canManageWorkflowSlack' | 'hasProfileTarget' | 'slackSelection'
  | 'slackConnName' | 'setSlackConnName' | 'slackConnBot' | 'setSlackConnBot' | 'slackConnApp' | 'setSlackConnApp'
  | 'slackConnEnabled' | 'setSlackConnEnabled' | 'slackConnSaving' | 'slackConnTesting' | 'slackConnTestResult'
  | 'slackConnConfirmDelete' | 'slackConnHasChanges' | 'saveWorkflowSlackConnection'
  | 'testWorkflowSlackConnection' | 'removeWorkflowSlackConnection'
  | 'workflowRoutes' | 'routeError' | 'newSlackChannel' | 'setNewSlackChannel' | 'addSlackRoute'
  | 'routeSaving' | 'myRoutes' | 'addError' | 'setAddError'
  | 'expandedChip' | 'setExpandedChip' | 'removeRoute' | 'updateRoute'
  | 'myOtherBots' | 'myBotRoutesHere' | 'newMyBotChannel' | 'setNewMyBotChannel'
  | 'myBotSaving' | 'myBotError' | 'setMyBotError' | 'addMyBotChannel' | 'removeMyBotChannel'
  | 'slackTarget' | 'reloadMyBots'
>


type AnsweringBot = { name: string; status: { label: string; ok: boolean }; connectionId: string | null }

function usableBotStatus(bot: SlackUsableBot): { label: string; ok: boolean } {
  if (!bot.configured) return { label: 'Missing tokens', ok: false }
  if (!bot.enabled) return { label: 'Disabled', ok: false }
  return { label: 'Ready', ok: true }
}

function CopyLine({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="w-20 shrink-0 text-xs text-muted-foreground">{label}</span>
      <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-xs text-foreground" title={text}>{text}</code>
      <Button
        type="button"
        variant="ghost"
        size="xs"
        aria-label={`Copy ${text}`}
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(text)
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          } catch { /* select and copy by hand */ }
        }}
      >
        {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      </Button>
    </div>
  )
}

export function SlackSetup({ bots, headerAction, homeTabAction, ownBotOnly = false }: {
  bots: SlackSetupBots
  headerAction?: ReactNode
  homeTabAction?: ReactNode
  /** A Code: 1:1 DMs only, never channels. */
  ownBotOnly?: boolean
}) {
  const {
    readOnly, workflowId, slackOriginal, slackLoading, slackError, slackSuccess,
    canManageWorkflowSlack, hasProfileTarget, slackSelection,
    workflowRoutes, routeError, myOtherBots, myBotRoutesHere,
  } = bots
  // The target's real kind (a Crew, a Code or a workflow), never "project".
  const targetProfile = (bots.slackTarget?.profile_id || '').toLowerCase()
  const noun = ownBotOnly || targetProfile === 'code' ? 'Code' : targetProfile === 'work' || hasProfileTarget ? 'Crew' : 'workflow'
  const own = slackSelection.own
  const shared = (slackOriginal.connections || []).find(conn => conn.is_default) || null
  const slackRoutes = workflowRoutes.filter(route => route.kind === 'slack')
  const ownTitle = canManageWorkflowSlack ? undefined : (readOnly ? READ_ONLY_TITLE : OWNER_ONLY_TITLE)
  const target = bots.slackTarget
  const targetKey = target ? `${target.profile_id || ''}|${target.workspace_path}` : ''

  // This target's slug and platform-bot state (the platform bot only exists
  // on servers that have one; RTS has own bots only).
  const [settings, setSettings] = useState<SlackTargetSettings | null>(null)
  useEffect(() => {
    if (!workflowId || !target?.workspace_path) return
    let live = true
    agentApi.getSlackTargetSettings(target).then(next => { if (live) setSettings(next) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflowId, targetKey])

  const attachedHere = (bot: SlackUsableBot) => !!target && (bot.targets || []).some(entry =>
    sameBotWorkspacePath(entry.workspace_path, target.workspace_path) && (entry.profile_id || '') === (target.profile_id || ''))
  const sharingBots = myOtherBots.filter(bot => myBotRoutesHere(bot).length > 0 || attachedHere(bot))
  const [pickedBotId, setPickedBotId] = useState<string | null>(null)
  const viaBot = own ? null : (myOtherBots.find(bot => bot.id === pickedBotId) || sharingBots[0] || null)
  const platformAvailable = !!settings?.platform_available
  const platformOn = platformAvailable && !!settings?.platform_bot
  const answering: AnsweringBot | null = own
    ? { name: own.display_name, status: connStatus(own), connectionId: own.id }
    : viaBot
      ? { name: viaBot.display_name, status: usableBotStatus(viaBot), connectionId: viaBot.id }
      : platformOn
        ? { name: settings?.platform_name || 'platform bot', status: { label: 'Ready', ok: true }, connectionId: null }
        : null

  // Offer the shared (platform) bot only when an admin has set one up, or
  // when this target already has shared-bot channels to manage.
  const showShared = !!shared?.configured || slackRoutes.length > 0 || platformAvailable
  const [mode, setMode] = useState<SlackMode>(own ? 'own' : sharingBots.length > 0 ? 'mine' : (slackRoutes.length > 0 ? 'shared' : 'own'))
  const ownId = own?.id || null
  const hasSharingBots = sharingBots.length > 0
  useEffect(() => {
    if (ownId) setMode('own')
    else if (hasSharingBots) setMode(prev => prev === 'own' ? 'mine' : prev)
  }, [ownId, hasSharingBots])
  const [changing, setChanging] = useState(false)
  const [editing, setEditing] = useState(false)
  useEffect(() => { setEditing(false) }, [ownId])
  const showChooser = changing || !answering
  const ownFormOpen = showChooser && mode === 'own' && (editing || !own)

  const [slugDraft, setSlugDraft] = useState('')
  const [slugError, setSlugError] = useState<string | null>(null)
  const [slugSaving, setSlugSaving] = useState(false)
  useEffect(() => { setSlugDraft(settings?.slug || '') }, [settings?.slug])
  const saveSlug = async () => {
    if (!target) return
    setSlugSaving(true)
    setSlugError(null)
    try {
      setSettings(await agentApi.updateSlackTargetSettings(target, { slug: slugDraft }))
    } catch (err) {
      setSlugError(slackErrorText(err, 'The slug was not saved'))
    } finally {
      setSlugSaving(false)
    }
  }

  if (slackLoading) {
    return <div className="flex items-center justify-center py-12"><Loader2 className="h-8 w-8 animate-spin text-primary" /></div>
  }

  const slug = settings?.slug || ''
  const canEditSlug = !!settings?.can_manage && !readOnly

  return (
    <div className="space-y-4">
      {/* While the own-bot form is open its errors show beside Save bot
          instead, where the user is looking (#201 sub-issue 6). */}
      {slackError && !ownFormOpen && <StatusBanner tone="error">{slackError}</StatusBanner>}
      {slackSuccess && <StatusBanner tone="success">{slackSuccess}</StatusBanner>}

      <FormSection title="Which bot answers" actions={headerAction}>
        {answering && !changing ? (
          <div className="flex items-center gap-2 text-sm">
            <span className="font-medium text-foreground">{answering.name}</span>
            <StatusDot ok={answering.status.ok} label={answering.status.label} />
            <Button variant="ghost" size="xs" className="ml-auto" onClick={() => setChanging(true)} disabled={readOnly} title={readOnly ? READ_ONLY_TITLE : undefined}>Change</Button>
          </div>
        ) : (
          <>
            {ownBotOnly && <p className="text-xs text-muted-foreground">A Code answers 1:1 direct messages only, for its owner, never in channels.</p>}
            <div className="flex flex-col gap-2 sm:flex-row">
              <ModeOption
                checked={mode === 'own'}
                disabled={false}
                onSelect={() => setMode('own')}
                icon={<MessageSquare className="h-3.5 w-3.5" />}
                label={`Its own bot${own ? ` · ${own.display_name}` : ''}`}
                hint={`A Slack app just for this ${noun}.`}
              />
              <ModeOption
                checked={mode === 'mine'}
                disabled={false}
                onSelect={() => setMode('mine')}
                icon={<Bot className="h-3.5 w-3.5" />}
                label={`One of my bots${sharingBots.length === 1 ? ` · ${sharingBots[0].display_name}` : ''}`}
                hint={`A bot you set up for another workflow or crew; people pick this ${noun} by its slug.`}
              />
              {showShared && (
                <ModeOption
                  checked={mode === 'shared'}
                  disabled={false}
                  onSelect={() => setMode('shared')}
                  icon={<Users className="h-3.5 w-3.5" />}
                  label={`Shared bot${shared ? ` · ${shared.display_name}` : ''}`}
                  hint="The platform bot an admin manages."
                />
              )}
            </div>
            {answering && <div className="flex justify-end"><Button variant="outline" size="sm" onClick={() => setChanging(false)}>Done</Button></div>}
          </>
        )}
      </FormSection>

      {showChooser && mode === 'own' && <OwnBotSection bots={bots} noun={noun} ownTitle={ownTitle} editing={editing || !own} onEdit={setEditing} homeTabAction={homeTabAction} directMessagesOnly={ownBotOnly} />}
      {showChooser && mode === 'mine' && (
        <MyBotsPicker bots={bots} noun={noun} dmOnly={ownBotOnly} attachedHere={attachedHere} onPick={id => { setPickedBotId(id); setChanging(false) }} />
      )}
      {showChooser && mode === 'shared' && showShared && (
        <>
          {!ownBotOnly && <SharedBotSection bots={bots} noun={noun} ownTitle={ownTitle} shared={shared} />}
          {settings && target && !platformOn && <SlackSlugsSection destination={target} noun={noun} readOnly={readOnly} dmOnly={ownBotOnly} settings={settings} onSettings={setSettings} />}
        </>
      )}

      {answering && settings && target && (
        <FormSection
          title="Slug"
          description={ownBotOnly
            ? `A short name for this ${noun}. In a DM, start your message with it.`
            : `A short name for this ${noun}. In a channel shared with others, put it after @${answering.name} to reach this one; in a DM, start with it.`}
          actions={
            <div className="flex items-center gap-2">
              <Input
                aria-label="Slack slug"
                value={slugDraft}
                onChange={e => setSlugDraft(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))}
                disabled={!canEditSlug || slugSaving}
                title={canEditSlug ? undefined : `Only the ${noun}'s owner can change the slug`}
                className="h-8 w-40 font-mono text-xs"
              />
              {slugDraft !== slug && (
                <Button variant="outline" size="sm" onClick={() => void saveSlug()} disabled={!canEditSlug || slugSaving || !slugDraft}>
                  {slugSaving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : 'Save'}
                </Button>
              )}
            </div>
          }
        >
          <CopyLine label="Example" text={ownBotOnly ? `${slug || 'slug'} your question` : `@${answering.name} ${slug || 'slug'} your question`} />
          {slugError && <p className="text-xs text-red-600 dark:text-red-400">{slugError}</p>}
        </FormSection>
      )}

      {answering && target && (
        <FormSection title="Where it answers">
          {answering.connectionId
            ? <SlackBotWhere botId={answering.connectionId} botName={answering.name} destination={target} dmOnly={ownBotOnly} />
            : settings && <SlackSlugsSection destination={target} noun={noun} readOnly={readOnly} dmOnly={ownBotOnly} settings={settings} onSettings={setSettings} />}
          {!ownBotOnly && slackRoutes.length > 0 && (
            <div className="grid gap-2">
              {slackRoutes.map(route => <RouteChip key={routeId(route)} bots={bots} route={route} />)}
            </div>
          )}
          {!ownBotOnly && <p className="text-[11px] text-muted-foreground">With several on one bot, people add the slug, or pick from buttons.</p>}
        </FormSection>
      )}

      {own && !showChooser && (
        <OwnBotSection bots={bots} noun={noun} ownTitle={ownTitle} editing={false} onEdit={editing => { setEditing(editing); if (editing) { setMode('own'); setChanging(true) } }} homeTabAction={homeTabAction} directMessagesOnly={ownBotOnly} />
      )}
      {platformOn && settings && target && ownBotOnly && <SlackSlugsSection destination={target} noun={noun} readOnly={readOnly} dmOnly settings={settings} onSettings={setSettings} />}
      {routeError && <StatusBanner tone="error">{routeError}</StatusBanner>}

      {!workflowId && <p className="text-xs text-muted-foreground">Select a {noun} to set up Slack.</p>}
    </div>
  )
}

function OwnBotSection({ bots, noun, ownTitle, editing, onEdit, homeTabAction, directMessagesOnly = false }: {
  bots: SlackSetupBots; noun: string; ownTitle?: string; editing: boolean; onEdit: (editing: boolean) => void; homeTabAction?: ReactNode; directMessagesOnly?: boolean
}) {
  const {
    canManageWorkflowSlack, slackSelection, slackConnConfirmDelete, removeWorkflowSlackConnection,
    slackConnName, setSlackConnName, slackConnBot, setSlackConnBot, slackConnApp, setSlackConnApp,
    slackConnEnabled, setSlackConnEnabled, slackConnSaving, slackConnTesting, slackConnTestResult,
    slackConnHasChanges, saveWorkflowSlackConnection, testWorkflowSlackConnection, slackAppDefaultName,
    slackError,
  } = bots
  const own = slackSelection.own
  const inviteHint = directMessagesOnly
    ? <>People with access open a direct message with <code className="rounded bg-muted px-1 font-mono">@{own?.display_name || 'YourBot'}</code> in Slack. It does not answer in channels.</>
    : <>Invite it to a channel with <code className="rounded bg-muted px-1 font-mono">/invite @{own?.display_name || 'YourBot'}</code>, then @mention it.</>

  if (own && !editing) {
    const status = connStatus(own)
    return (
      <FormSection
        title={<span className="flex items-center gap-2">{own.display_name}<StatusDot ok={status.ok} label={status.label} /></span>}
        description={inviteHint}
        actions={<Button variant="ghost" size="xs" onClick={() => onEdit(true)} disabled={!canManageWorkflowSlack} title={ownTitle}>Edit</Button>}
      >
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void testWorkflowSlackConnection()} disabled={!canManageWorkflowSlack || slackConnTesting || slackConnSaving} title={ownTitle}>
            {slackConnTesting ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Testing...</> : 'Test bot'}
          </Button>
          <Button variant="outline" onClick={() => void removeWorkflowSlackConnection()} disabled={!canManageWorkflowSlack || slackConnSaving || slackConnTesting} title={ownTitle} className="ml-auto">
            {slackConnConfirmDelete ? 'Click again to remove' : 'Remove bot'}
          </Button>
        </div>
        {homeTabAction && canManageWorkflowSlack && (
          <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-muted/20 px-3 py-2">
            <p className="text-xs text-muted-foreground">
              <b className="text-foreground">Home tab</b> — what people see when they open the app in Slack. Turn on <b>App Home → Home Tab</b> in the Slack app first.
            </p>
            {homeTabAction}
          </div>
        )}
        {slackConnTestResult && <SlackChecksView result={slackConnTestResult} />}
        {/* The setup help stays reachable once the bot is installed: new
            features (e.g. direct messages) need scopes and events added in
            the Slack app, then a reinstall. */}
        <details className="rounded-md border border-border bg-muted/20 px-3 py-2 text-xs">
          <summary className="cursor-pointer select-none font-medium text-foreground">Update the Slack app's permissions</summary>
          <p className="mt-2 text-muted-foreground">
            Open the app at <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer" className="underline">api.slack.com/apps</a>, add anything missing below, then reinstall it to the workspace. <b>Test bot</b> reports what the installed token has.
          </p>
          <div className="mt-3">
            <SlackPermissionsChecklist />
          </div>
        </details>
      </FormSection>
    )
  }

  return (
    <FormSection title={own ? `Edit ${own.display_name}` : `Set up this ${noun}'s bot`} description={<>Create a Slack app for this {noun} and paste its two tokens. {inviteHint}</>}>
      <SlackManifestSetup defaultName={slackConnName || slackAppDefaultName} finalStep={<>Save, then in Slack run <b>/invite @YourBot</b> in any channel and @mention it.</>} />
      <SlackAppSetupSteps finalStep={<>Save below, then in Slack run <b>/invite @YourBot</b> in any channel and @mention it.</>} />
      <SlackPermissionsChecklist />
      <div>
        <Label className="mb-2 block">Bot name</Label>
        <Input type="text" value={slackConnName} onChange={e => setSlackConnName(e.target.value)} disabled={!canManageWorkflowSlack} placeholder="e.g. Support bot" title={ownTitle} />
      </div>
      <SecretField label="Bot Token" hint={<SlackTokenHint kind="bot" />} value={slackConnBot} onChange={setSlackConnBot} disabled={!canManageWorkflowSlack} placeholder="xoxb-..." disabledTitle={ownTitle} />
      <SecretField label="App Token (Socket Mode)" hint={<SlackTokenHint kind="app" />} value={slackConnApp} onChange={setSlackConnApp} disabled={!canManageWorkflowSlack} placeholder="xapp-..." disabledTitle={ownTitle} />
      {own && (
        <ToggleRow label="Bot enabled" checked={slackConnEnabled} onCheckedChange={setSlackConnEnabled} disabled={!canManageWorkflowSlack} disabledTitle={ownTitle} />
      )}
      <div className="flex items-center gap-2">
        <Button onClick={() => void saveWorkflowSlackConnection()} disabled={!canManageWorkflowSlack || !slackConnHasChanges || slackConnSaving || slackConnTesting} title={ownTitle} className="flex items-center gap-2">
          {slackConnSaving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving...</> : <><CheckCircle className="h-4 w-4" />Save bot</>}
        </Button>
        <Button variant="outline" onClick={() => void testWorkflowSlackConnection()} disabled={!canManageWorkflowSlack || slackConnTesting || slackConnSaving} title={ownTitle}>
          {slackConnTesting ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Testing...</> : 'Save & test'}
        </Button>
        {own && (
          <Button variant="outline" onClick={() => onEdit(false)} disabled={slackConnSaving || slackConnTesting} className="ml-auto">
            Done
          </Button>
        )}
      </div>
      {/* Repeated beside the buttons: the tab's top banner is scrolled out of
          view by the time Save is pressed (#201 sub-issue 6). */}
      {slackError && <StatusBanner tone="error">{slackError}</StatusBanner>}
      {slackConnTestResult && <SlackChecksView result={slackConnTestResult} />}
    </FormSection>
  )
}

function SharedBotSection({ bots, noun, ownTitle, shared }: {
  bots: SlackSetupBots; noun: string; ownTitle?: string; shared: SlackConnection | null
}) {
  const {
    readOnly, workflowId, canManageWorkflowSlack, canManageSlackDefault, loadSlack, slackSelection, slackConnSaving, slackConnConfirmDelete,
    removeWorkflowSlackConnection, workflowRoutes, newSlackChannel, setNewSlackChannel, addSlackRoute,
    routeSaving, myRoutes, addError, setAddError,
  } = bots
  const own = slackSelection.own
  const routes = workflowRoutes.filter(route => route.kind === 'slack')
  const status = shared ? connStatus(shared) : { label: canManageSlackDefault ? 'Not set up · see shared bot settings below' : 'Not set up · ask an admin (Access → Slack)', ok: false }
  const adding = routeSaving?.startsWith('slack:') && !myRoutes.some(route => routeId(route) === routeSaving)
  const error = addError.slack

  return (
    <FormSection
      title={<span className="flex items-center gap-2">{shared?.display_name || 'Shared bot'}<StatusDot ok={status.ok} label={status.label} /></span>}
      description={`Add the channels where the shared bot answers for this ${noun}. Invite the bot there, then find the channel ID (starts with C) under View channel details.`}
    >
      {own && (
        <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-200">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span className="flex-1">This {noun}'s own bot <b>{own.display_name}</b> still answers wherever it's invited. Remove it to use only the shared bot.</span>
          <Button variant="link" size="xs" onClick={() => void removeWorkflowSlackConnection()} disabled={!canManageWorkflowSlack || slackConnSaving} title={ownTitle} className="h-auto shrink-0 p-0 text-amber-800 dark:text-amber-200">
            {slackConnConfirmDelete ? 'Click again to remove' : 'Remove own bot'}
          </Button>
        </div>
      )}
      {workflowId && (
        <div className="flex items-center gap-2">
          <Input
            type="text"
            value={newSlackChannel}
            onChange={e => {
              setNewSlackChannel(e.target.value.toUpperCase().replace(/[^A-Z0-9_,;\s-]/g, ''))
              if (error) setAddError(prev => ({ ...prev, slack: undefined }))
            }}
            onKeyDown={e => { if (e.key === 'Enter') addSlackRoute() }}
            placeholder="channel ID, e.g. C1234567890"
            disabled={readOnly || !!adding}
            title={readOnly ? READ_ONLY_TITLE : undefined}
            className="h-8 min-w-0 flex-1 font-mono text-xs"
          />
          <Button variant="outline" size="sm" onClick={addSlackRoute} disabled={readOnly || !newSlackChannel.trim() || !!adding} title={readOnly ? READ_ONLY_TITLE : undefined}>
            {adding ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
            Add channel
          </Button>
        </div>
      )}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
      {routes.length > 0 ? (
        <div className="grid gap-2">
          {routes.map(route => <RouteChip key={routeId(route)} bots={bots} route={route} />)}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">No channels yet.</p>
      )}
      {canManageSlackDefault && (
        <details className="border-t border-border pt-2 text-xs">
          <summary className="cursor-pointer select-none font-medium text-muted-foreground">Shared bot settings (admin)</summary>
          <div className="mt-3">
            <SharedSlackBotSettings onSaved={() => void loadSlack()} />
          </div>
        </details>
      )}
    </FormSection>
  )
}

function MyBotsPicker({ bots, noun, dmOnly, attachedHere, onPick }: {
  bots: SlackSetupBots; noun: string; dmOnly: boolean; attachedHere: (bot: SlackUsableBot) => boolean; onPick: (id: string) => void
}) {
  const { readOnly, myOtherBots, myBotRoutesHere, slackTarget, reloadMyBots } = bots
  const preferred = myOtherBots.find(bot => myBotRoutesHere(bot).length > 0 || attachedHere(bot)) || myOtherBots[0] || null
  const [pickedId, setPickedId] = useState<string>(preferred?.id || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const picked = myOtherBots.find(bot => bot.id === pickedId) || preferred

  if (myOtherBots.length === 0) {
    return (
      <FormSection title="One of my bots" description={`Use a bot you already set up for another workflow or crew, so one Slack app answers for several of them.`}>
        <p className="text-xs text-muted-foreground">
          You have no other bots yet. Give another workflow or crew its own bot first, then pick it here.
        </p>
      </FormSection>
    )
  }
  const use = async () => {
    if (!picked) return
    // Attaching a target to a bot is the only grant: it then answers wherever
    // the bot is (Codes: DMs only), picked by its slug.
    setBusy(true)
    setError(null)
    try {
      await agentApi.attachSlackBotTarget(picked.id, slackTarget)
      await reloadMyBots()
      onPick(picked.id)
    } catch (err) {
      setError(slackErrorText(err, 'Could not use this bot'))
    } finally {
      setBusy(false)
    }
  }
  return (
    <FormSection title="One of my bots" description={dmOnly ? `People DM the bot and start with this ${noun}'s slug.` : `People pick this ${noun} with the bot's name and its slug. You choose the channels next.`}>
      <div className="flex items-center gap-2">
        <select
          aria-label="One of my bots"
          value={picked?.id || ''}
          onChange={e => { setPickedId(e.target.value); setError(null) }}
          className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm"
          disabled={readOnly || busy}
        >
          {myOtherBots.map(bot => <option key={bot.id} value={bot.id}>{bot.display_name}{bot.owner_label ? ` (set up for ${bot.owner_label})` : ''}</option>)}
        </select>
        <Button variant="outline" size="sm" onClick={() => void use()} disabled={readOnly || busy || !picked}>
          {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
          Use this bot
        </Button>
      </div>
      {myOtherBots.length === 1 && picked?.owner_label && <p className="text-xs text-muted-foreground">Set up for {picked.owner_label}.</p>}
      {myOtherBots.filter(attachedHere).map(bot => (
        <div key={bot.id} className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="flex-1">Answers {dmOnly ? 'DMs ' : ''}through {bot.display_name}</span>
          <Button
            variant="ghost"
            size="xs"
            aria-label={`Stop answering through ${bot.display_name}`}
            disabled={readOnly || busy}
            onClick={async () => {
              setBusy(true)
              try { await agentApi.detachSlackBotTarget(bot.id, slackTarget); await reloadMyBots() } catch (err) { setError(slackErrorText(err, 'Could not stop it')) } finally { setBusy(false) }
            }}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
    </FormSection>
  )
}
