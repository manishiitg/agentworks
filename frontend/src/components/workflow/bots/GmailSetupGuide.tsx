import { useState } from 'react'
import { ChevronRight, ExternalLink } from 'lucide-react'
import { getApiBaseUrl } from '../../../services/api'
import { resolveGmailOAuthCallbackUrl } from './gmailOAuthCallback'

// ── First-time Google setup, as a guide ───────────────────────────────────
//
// Connecting a mailbox needs a Google Cloud OAuth client, and Google provides
// no API to create one — it is Console-only. So this cannot be automated; it
// is written as instructions the reader follows themselves.
//
// The steps are ordered by what actually blocks a first-time setup, and each
// gotcha listed here is one that produces a confusing failure rather than a
// clear error message.
//
// The active installation reports whether it sends through gog or gws. The
// Google Cloud project and OAuth client steps are the same for both.

const linkClass =
  'inline-flex items-center gap-1 text-primary underline underline-offset-2 hover:no-underline'

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="grid grid-cols-[1.5rem_1fr] gap-x-2">
      <span className="flex h-5 w-5 items-center justify-center rounded-full bg-muted text-[11px] font-medium text-muted-foreground">
        {n}
      </span>
      <div className="space-y-1.5">
        <p className="text-xs font-medium text-foreground">{title}</p>
        <div className="space-y-1.5 text-xs text-muted-foreground">{children}</div>
      </div>
    </li>
  )
}

function Cmd({ children }: { children: React.ReactNode }) {
  return (
    <pre className="overflow-x-auto rounded border border-border bg-muted/40 px-2 py-1.5 font-mono text-[11px] text-foreground">
      {children}
    </pre>
  )
}

// Gotchas are called out separately from steps: each one is a mistake that
// produces a misleading symptom later, not an error at the point you make it.
function Gotcha({ children }: { children: React.ReactNode }) {
  return (
    <p className="rounded border border-amber-300/60 bg-amber-50 px-2 py-1.5 text-[11px] text-amber-900 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-100">
      {children}
    </p>
  )
}

export function GmailSetupGuide({ backend }: { backend?: string }) {
  const [open, setOpen] = useState(false)
  const usesGog = backend === 'gog'
  const callbackUrl = resolveGmailOAuthCallbackUrl(
    getApiBaseUrl(),
    typeof window === 'undefined' ? '' : window.location.origin,
  )

  return (
    <div className="rounded-md border border-border">
      <button
        type="button"
        onClick={() => setOpen(value => !value)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-medium text-foreground transition-colors hover:bg-muted/50"
      >
        <ChevronRight className={`h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform ${open ? 'rotate-90' : ''}`} />
        First-time setup guide
        <span className="font-normal text-muted-foreground">— for your own OAuth JSON</span>
      </button>

      {open && (
        <div className="space-y-4 border-t border-border p-3">
          <p className="text-xs text-muted-foreground">
            Sending mail needs a Google Cloud OAuth client. Google has no API for creating one, so these
            steps are done by hand in the Cloud Console. Each client you register below gets its own
            name — reuse a Google Cloud project (and its client) for every mailbox that should share it,
            or create a new one if you want a separate project. A second upload never silently replaces
            an existing client; it needs its own name.
          </p>

          <ol className="space-y-3">
            <Step n={1} title="Check the Gmail CLI on the server">
              <p>Mail is sent through <code>{usesGog ? 'gog' : 'gws'}</code>, which must be installed on the AgentWorks server&rsquo;s PATH.</p>
              {usesGog ? <>
                <Cmd>brew install openclaw/tap/gogcli</Cmd>
                <p>
                  No Homebrew on the box? Download a prebuilt binary from the{' '}
                  <a className={linkClass} href="https://github.com/openclaw/gogcli/releases" target="_blank" rel="noreferrer">
                    releases page <ExternalLink className="h-3 w-3" />
                  </a>{' '}
                  and put it on PATH.
                </p>
              </> : <p>Ask the server operator to install <code>@googleworkspace/cli</code> if the account status reports that it is missing.</p>}
            </Step>

            <Step n={2} title="Pick or create a Google Cloud project">
              <p>
                Any project works, including a new empty one.{' '}
                <a className={linkClass} href="https://console.cloud.google.com/projectcreate" target="_blank" rel="noreferrer">
                  Create a project <ExternalLink className="h-3 w-3" />
                </a>
              </p>
              <p>Note its ID — later steps and error messages refer to it.</p>
            </Step>

            <Step n={3} title="Enable the Gmail API on that project">
              <p>
                <a className={linkClass} href="https://console.cloud.google.com/apis/library/gmail.googleapis.com" target="_blank" rel="noreferrer">
                  Gmail API in the library <ExternalLink className="h-3 w-3" />
                </a>{' '}
                → <strong>Enable</strong>. Check the project selector at the top matches the project from step 2.
              </p>
            </Step>

            <Step n={4} title="Configure the consent screen">
              <p>
                Under <strong>APIs &amp; Services → OAuth consent screen</strong> (newer Console versions call this{' '}
                <strong>Google Auth Platform</strong>), choose <strong>External</strong> and fill in an app name and contact email.
              </p>
              <Gotcha>
                While publishing status is <strong>Testing</strong>, only addresses listed under <strong>Test users</strong> can
                sign in — anyone else gets a generic &ldquo;Access blocked&rdquo; with no explanation. Add every mailbox you plan
                to connect, or publish the app so the list no longer applies.
              </Gotcha>
              <Gotcha>
                Requesting a scope in code is not enough — Google only shows a scope on the consent screen (and
                only grants it) if it is also added under <strong>Data access</strong> (older Console: still the{' '}
                <strong>Scopes</strong> step) on this same OAuth consent screen. A project reused from something
                else, or one where this step was skipped, can silently drop a requested scope. Add{' '}
                <code>gmail.send</code> for notifications; add <code>gmail.readonly</code> only if you select mailbox
                reading, <code>gmail.compose</code> only if you allow agent drafts and replies, and the matching
                scopes for any Google Workspace services you select below. Otherwise sign-in can seem to succeed
                while the requested capability remains unavailable.
              </Gotcha>
            </Step>

            <Step n={5} title="Create the OAuth client">
              <p>
                <a className={linkClass} href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noreferrer">
                  Credentials <ExternalLink className="h-3 w-3" />
                </a>{' '}
                → <strong>Create Credentials</strong> → <strong>OAuth client ID</strong> → download the JSON.
              </p>
              <Gotcha>
                Which Google OAuth client type to pick depends on where <em>this AgentWorks server</em> runs, not
                where your browser is:
              </Gotcha>
              <p>
                <strong>AgentWorks running on a real server</strong> (any hosted deployment reachable
                at a public HTTPS address) — create a <strong>Web application</strong> client, and add the exact
                callback below under <strong>Authorized redirect URIs</strong>. Google rejects a{' '}
                <strong>Desktop app</strong> client here with <code>400: redirect_uri_mismatch</code>, since a
                Desktop client can&rsquo;t register a real HTTPS redirect at all.
              </p>
              <p>
                <strong>AgentWorks running locally</strong> (the callback resolves to <code>localhost</code>/
                <code>127.0.0.1</code>) — a <strong>Desktop app</strong> client also works, with nothing to add
                under Authorized redirect URIs: Google&rsquo;s loopback exemption matches any path on that host and
                port. <strong>Web application</strong> works locally too, as long as you still add the exact
                callback below as an authorized redirect URI — so when in doubt, Web application is the choice
                that works either way.
              </p>
              <Cmd>{callbackUrl}</Cmd>
              <p>
                This address follows the active environment automatically: the current site origin on web, or the
                configured API server when AgentWorks is running in Desktop.
              </p>
            </Step>

            <Step n={6} title="Check the downloaded client file">
              <p>
                Keep the downloaded JSON file ready for <strong>Use my own OAuth JSON</strong>.
                You do not need to place it on the server filesystem.
              </p>
              <p>
                Verify it is the type you meant to create — the top-level key reads <code>web</code> for a Web
                application client, or <code>installed</code> for a Desktop app client. Either is fine locally;
                only <code>web</code> works against a real hosted server.
              </p>
            </Step>

            <Step n={7} title="Add each signing-in address as a test user">
              <p>
                While the app is in <strong>Testing</strong>, Google permits only the addresses listed under
                <strong> Google Auth Platform → Audience → Test users</strong> to authorize it. Add every mailbox
                that will sign in.
              </p>
              <Gotcha>
                <strong>Internal</strong> does not mean &ldquo;all company addresses you choose.&rdquo; It permits only
                users in the Google Workspace organization that owns this Cloud project. If the mailbox is in a
                different organization, choose <strong>External + Testing</strong> and add it as a test user.
              </Gotcha>
            </Step>

            <Step n={8} title="Add your mailboxes">
              <p>
                In <strong>Connect a Google account</strong>, select <strong>Use my own OAuth JSON</strong>, name the app and upload its JSON,
                choose the access it needs, and click <strong>Connect Google account</strong>. Complete Google consent in
                the browser. Reuse the saved app in <strong>Connect a Google account</strong> for another mailbox; you can reuse the same Google
                Cloud project when appropriate.
              </p>
              <Gotcha>
                The tab opens in whichever Chrome profile is frontmost. If the mailbox belongs to a different
                profile, open that profile first and use <strong>Copy link</strong> on the row to paste the sign-in
                link into it. Do not copy the URL out of the address bar of the tab that opened — by then Chrome has
                followed Google&rsquo;s redirects, and that URL carries a token tied to the profile it started in.
                Pasting it elsewhere fails with a bare{' '}
                <code>400. That&rsquo;s an error. The server cannot process the request because it is malformed.</code>
              </Gotcha>
            </Step>
          </ol>

          <p className="border-t border-border pt-3 text-[11px] text-muted-foreground">
            <strong>If a sign-in seems to succeed but shows the wrong address:</strong> that account&rsquo;s cached
            access token is still the previous one. Use <strong>Reconnect</strong> on the row rather than adding a
            second entry.
          </p>
        </div>
      )}
    </div>
  )
}
