import { useMemo, useState, type ReactNode } from 'react'
import { AlertCircle, Check, CheckCircle, Copy } from 'lucide-react'
import { Button } from '../../ui/Button'
import { Input } from '../../ui/Input'
import { Label } from '../../ui/label'
import type { SlackTestResponse } from '../../../services/api-types'
import {
  SLACK_APP_TOKEN_SCOPE, SLACK_BOT_EVENTS, SLACK_DEFAULT_APP_NAME, SLACK_INTERACTIVITY_REQUIRED,
  SLACK_DM_EVENTS, SLACK_DM_SCOPES, SLACK_OPTIONAL_SCOPES, SLACK_REQUIRED_SCOPE_GROUPS, slackAppManifestJSON,
} from './slackAppManifest'

// Shared by a workflow's own bot form and the admin's shared bot form:
// how to create a Slack app, and the result of a token/scope test.

const Code = ({ children }: { children: ReactNode }) => (
  <code className="rounded bg-muted px-1 font-mono">{children}</code>
)

export function SlackTokenHint({ kind }: { kind: 'bot' | 'app' }) {
  return <>
    In your <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer" className="underline">Slack app settings</a>,{' '}
    {kind === 'bot'
      ? <>open <b>OAuth &amp; Permissions → OAuth Tokens</b>. Install the app to your workspace, then copy the <b>Bot User OAuth Token</b> (<Code>xoxb-</Code>).</>
      : <>open <b>Basic Information → App-Level Tokens</b>. Generate a token with <Code>connections:write</Code> and copy it (<Code>xapp-</Code>). Enable <b>Socket Mode</b> too.</>}
  </>
}

export function SlackPermissionsChecklist({ directMessagesOnly = false }: { directMessagesOnly?: boolean }) {
  const scopeGroups = directMessagesOnly
    ? SLACK_REQUIRED_SCOPE_GROUPS.filter(group => group.label !== 'Read mentions and channel context')
    : SLACK_REQUIRED_SCOPE_GROUPS
  const optionalScopes = directMessagesOnly
    ? SLACK_OPTIONAL_SCOPES.filter(item => item.scope !== 'chat:write.public')
    : SLACK_OPTIONAL_SCOPES
  return (
    <section aria-label="Slack permissions and events" className="space-y-3 rounded-md border border-border bg-muted/20 p-3 text-xs text-muted-foreground">
      <div>
        <h4 className="font-semibold text-foreground">Permissions and events</h4>
        <p className="mt-1">In your Slack app, open <b>OAuth &amp; Permissions → Bot Token Scopes</b> and add these required scopes:</p>
      </div>
      <ul className="space-y-2">
        {scopeGroups.map(group => (
          <li key={group.label}><b className="text-foreground">{group.label}:</b> {codeList(group.scopes)}</li>
        ))}
      </ul>
      <p><b className="text-foreground">Optional bot scopes:</b> {optionalScopes.map((item, i) => <span key={item.scope}>{i > 0 && '; '}<Code>{item.scope}</Code> to {item.purpose}</span>)}.</p>
      <p><b className="text-foreground">App-Level Token:</b> add <Code>{SLACK_APP_TOKEN_SCOPE}</Code> under <b>Basic Information → App-Level Tokens</b> for Socket Mode.</p>
      {!directMessagesOnly && <p><b className="text-foreground">Required bot events:</b> under <b>Event Subscriptions → Subscribe to bot events</b>, add {codeList(SLACK_BOT_EVENTS)} and save. Enable Socket Mode first; leave Request URL empty.</p>}
      <p><b className="text-foreground">Direct messages (1:1 with the bot):</b> add the scopes {codeList(SLACK_DM_SCOPES)} and the event {codeList(SLACK_DM_EVENTS)}, and under <b>App Home</b> turn on the <b>Messages Tab</b> and tick <b>Allow users to send Slash commands and messages from the messages tab</b>. A DM runs with the sender's own AgentWorks permissions, matched by their Slack email{directMessagesOnly ? '.' : '; channels always run in Run mode.'}</p>
      {SLACK_INTERACTIVITY_REQUIRED && <p><b className="text-foreground">Interactivity:</b> turn on <b>Interactivity &amp; Shortcuts</b> so button clicks in the bot's replies reach it. With Socket Mode no Request URL is needed.</p>}
      <p>Reinstall the Slack app after changing scopes. “Save &amp; test” checks tokens and granted scopes; confirm delivery with {directMessagesOnly ? 'a direct message to the bot' : 'an @mention and a plain thread reply'}.</p>
    </section>
  )
}

function codeList(items: readonly string[]) {
  return items.map((item, i) => <span key={item}>{i > 0 && ' '}<Code>{item}</Code></span>)
}

// Create the Slack app from a ready-made manifest instead of setting scopes,
// events and Socket Mode by hand. Generated from the same lists as the
// checklist above.
export function SlackManifestSetup({ defaultName, finalStep, directMessagesOnly = false }: { defaultName?: string; finalStep: ReactNode; directMessagesOnly?: boolean }) {
  const fallbackName = defaultName?.trim() || SLACK_DEFAULT_APP_NAME
  const [name, setName] = useState('')
  const [optional, setOptional] = useState<string[]>([])
  const [copy, setCopy] = useState<'idle' | 'copied' | 'failed'>('idle')
  const json = useMemo(() => slackAppManifestJSON({ name: name.trim() || fallbackName, optionalScopes: optional }), [name, fallbackName, optional])

  const copyJSON = async () => {
    try {
      await navigator.clipboard.writeText(json)
      setCopy('copied')
    } catch {
      setCopy('failed')
    }
    setTimeout(() => setCopy('idle'), 2000)
  }

  return (
    <section aria-label="Create from manifest" className="space-y-3 rounded-md border border-border bg-muted/20 p-3 text-xs text-muted-foreground">
      <div>
        <h4 className="font-semibold text-foreground">Create from manifest</h4>
        <p className="mt-1">The quickest way: this manifest already sets every permission, event, Socket Mode and interactivity.</p>
      </div>
      <div>
        <Label htmlFor="slack-manifest-name" className="mb-1 block text-xs">Slack app name</Label>
        <Input id="slack-manifest-name" type="text" value={name} maxLength={35} onChange={e => setName(e.target.value)} placeholder={fallbackName} className="h-8 text-xs" />
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        {SLACK_OPTIONAL_SCOPES.filter(item => !directMessagesOnly || item.scope !== 'chat:write.public').map(item => (
          <label key={item.scope} className="flex cursor-pointer items-center gap-1.5">
            <input
              type="checkbox"
              checked={optional.includes(item.scope)}
              onChange={e => setOptional(prev => e.target.checked ? [...prev, item.scope] : prev.filter(scope => scope !== item.scope))}
            />
            Also {item.purpose} (<Code>{item.scope}</Code>)
          </label>
        ))}
      </div>
      <div className="relative">
        <pre aria-label="Slack app manifest" className="max-h-64 overflow-auto rounded-md border border-border bg-background p-2 font-mono text-[11px] leading-snug text-foreground">{json}</pre>
        <Button variant="outline" size="xs" onClick={() => void copyJSON()} className="absolute right-2 top-2">
          {copy === 'copied' ? <><Check className="h-3 w-3" />Copied</> : <><Copy className="h-3 w-3" />{copy === 'failed' ? 'Copy failed' : 'Copy'}</>}
        </Button>
      </div>
      <ol className="list-decimal space-y-2 pl-4">
        <li>Go to <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer" className="underline">api.slack.com/apps</a> → <b>Create New App</b> → <b>From a manifest</b>. Pick your workspace, choose <b>JSON</b>, paste the manifest and click <b>Create</b>.</li>
        <li><b>Basic Information</b> → <b>App-Level Tokens</b> → <b>Generate Token and Scopes</b>. Add <Code>{SLACK_APP_TOKEN_SCOPE}</Code> and copy the <Code>xapp-</Code> <b>App Token</b>.</li>
        <li><b>Install App</b> → <b>Install to Workspace</b>. Copy the <b>Bot User OAuth Token</b> (<Code>xoxb-</Code>) and paste both tokens below. {finalStep}</li>
      </ol>
    </section>
  )
}

export function SlackAppSetupSteps({ finalStep, directMessagesOnly = false }: { finalStep: ReactNode; directMessagesOnly?: boolean }) {
  return (
    <details className="rounded-md border border-border bg-muted/20 px-3 py-2 text-xs">
      <summary className="cursor-pointer select-none font-medium text-foreground">Where to get Slack tokens and set up the app by hand</summary>
      <ol className="mt-3 list-decimal space-y-3 pl-4 text-muted-foreground">
        <li>
          Go to <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer" className="underline">api.slack.com/apps</a> → <b>Create New App</b> → <b>From scratch</b>. Pick a name and your workspace.
        </li>
        <li>
          <b>OAuth &amp; Permissions</b> → <b>Bot Token Scopes</b>. Add the required scopes in the permissions card below, plus {codeList(SLACK_DM_SCOPES)} for direct messages. Add optional scopes only for features you need.
        </li>
        <li>
          <b>Basic Information</b> → <b>App-Level Tokens</b> → <b>Generate Token and Scopes</b>. Add <Code>connections:write</Code> and copy the <Code>xapp-</Code> <b>App Token</b>. Then enable <b>Socket Mode</b>.
        </li>
        <li>
          <b>Event Subscriptions</b> → enable and subscribe to the bot events {codeList(directMessagesOnly ? SLACK_DM_EVENTS : SLACK_BOT_EVENTS)}. All are required; leave the Request URL empty. {!directMessagesOnly && <>For direct messages also add {codeList(SLACK_DM_EVENTS)}.</>}
        </li>
        <li>
          For direct messages: <b>App Home</b> → turn on the <b>Messages Tab</b> and tick <b>Allow users to send Slash commands and messages from the messages tab</b>.
        </li>
        {SLACK_INTERACTIVITY_REQUIRED && <li><b>Interactivity &amp; Shortcuts</b> → turn it on. No Request URL is needed with Socket Mode.</li>}
        <li>
          <b>Install App</b> → <b>Install to Workspace</b>. Copy the <b>Bot User OAuth Token</b> from OAuth &amp; Permissions. It starts with <Code>xoxb-</Code>: that is the <b>Bot Token</b>.
        </li>
        <li>{finalStep}</li>
      </ol>
      <p className="mt-3 italic text-muted-foreground">After changing scopes or events later, reinstall the app.</p>
    </details>
  )
}

export function SlackChecksView({ result }: { result: SlackTestResponse }) {
  return (
    <div className={`flex items-start gap-2 rounded-lg border p-3 ${result.success ? 'border-green-200 bg-green-50 dark:border-green-800 dark:bg-green-900/20' : 'border-red-200 bg-red-50 dark:border-red-800 dark:bg-red-900/20'}`}>
      {result.success ? <CheckCircle className="mt-0.5 h-4 w-4 flex-shrink-0 text-green-600 dark:text-green-400" /> : <AlertCircle className="mt-0.5 h-4 w-4 flex-shrink-0 text-red-600 dark:text-red-400" />}
      <div className="min-w-0 space-y-2 text-sm">
        <p>{result.message}</p>
        {!!result.checks?.length && (
          <details open={!result.success}>
            <summary className="cursor-pointer font-medium">Setup checks</summary>
            <ul className="mt-2 space-y-2">
              {result.checks.map(check => (
                <li key={check.name}>
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium">{check.name}</span>
                    <span className={check.status === 'passed' ? 'text-green-600 dark:text-green-400' : check.status === 'manual' ? 'text-amber-600 dark:text-amber-400' : 'text-red-600 dark:text-red-400'}>
                      {check.status === 'manual' ? 'Verify manually' : check.status === 'passed' ? 'Passed' : check.status === 'missing' ? 'Missing' : 'Failed'}
                    </span>
                  </div>
                  {check.status !== 'passed' && <p className="text-xs text-muted-foreground">{check.message}</p>}
                </li>
              ))}
            </ul>
          </details>
        )}
      </div>
    </div>
  )
}
