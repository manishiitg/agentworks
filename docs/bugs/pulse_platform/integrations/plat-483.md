# PLAT-483: Builder-guided administrator setup for automatic incoming Gmail

State: deployed to RTS for testing; gateway review forwarding is verified on RTS; review submission and provisioning fixes in [PLAT-499](plat-499.md) await deployment. Cloud provisioning and real email delivery are not yet verified. No real Cloud resources or mailbox watches were changed during implementation.
Priority: P2.

## Problem

Connecting Gmail works through the browser, but automatic receiving previously required an operator to create Pub/Sub resources, add IAM grants, edit three server environment variables and restart. Builder could only recite the manual checklist or propose costly scheduled agent checks. Gog watchers still require Pub/Sub; they do not provision Cloud infrastructure.

## Change

- `setup_gmail_inbound(action=prepare|status)` is available through the shared Gmail Builder tool family for workflows, Crew and Code. Backend checks require an interactive app administrator and deny bot, email, scheduled/child, external-builder, scoped and PAT authority. The company OAuth app and named local apps are supported. Upload metadata retains the non-secret project ID; missing metadata leads to one question for the owning Cloud project ID.
- Preparation creates an immutable resource plan and expiring review link. The human opens the plan and completes fresh Cloud consent in Google. Provisioning requires a single-use code with plan-bound PKCE, reuses the existing selected app's OAuth callback, and never requests offline Cloud access. Cloud tokens are ephemeral and excluded from chat, logs, gog and persisted connection/configuration state.
- Provisioning verifies the OAuth project's number before mutations, enables APIs, creates/reuses a shared project topic and deployment-specific push identity/subscription, merges narrow Gmail publisher/Pub/Sub token-creator IAM bindings while preserving etags/conditions/unrelated grants, and verifies authenticated wrapped push delivery. Conflicting subscriptions are refused. Existing client topics and push identities are preserved, including subscriptions in the existing push identity's project for additional OAuth projects. No operator project roles or service account keys are created.
- Successful setup atomically saves mode-0600 configuration in the deployment's durable private state root. The same queue/worker pools and OIDC receiver activate without restarting the backend. Missing or conflicting configuration disables work and intake. Manual environment settings remain supported, but conflicts fail closed. Existing sender approvals, DMARC checks, rules, target ownership and delivery semantics remain.
- Setup is bounded, one job runs at a time, expired/used reviews cannot be reused, removed/replaced app plans are refused, admin revocation blocks subsequent Cloud requests and activation, and failures leave created resources available for an explicit retry rather than destructively rolling them back. Jobs/review links are in memory; successful configuration survives restart.
- The shared read-only pane displays setup progress, errors and the browser review link. Builder/Code skill variants and operator/owner guides distinguish automatic setup, optional manual setup, Cloud permissions, local HTTPS tunnels, mailbox readiness and real delivery testing.
- Right-panel Google apps headers in workflows, Crew and Code, the account card's setup button and the Incoming email action share the receiving-setup instructions. They inspect setup status, reuse pending consent links, prepare only when needed, and keep technical instructions hidden behind a plain-language Ask AI message. Relay account guidance retains its Google-app scope without offering incoming triggers.

## Verification

Simulated Google API tests cover resource creation, retries, cross-project delivery, preservation of conditional IAM bindings/etags, wrong OAuth-project rejection before mutation, conflicting subscriptions and credential-bearing error redaction. Server tests exercise preparation without mutations, authority rejection, review/consent with PKCE, cancellation/expiry/replay, stale app rejection, consent-to-provision-to-activation and permission-failure paths, private persistence/environment conflicts, and live activation with unsigned events rejected. Focused Gmail/server suites pass with the race detector. Gmail/worker/profile/product tests and 18 Incoming email UI tests pass; frontend types and full production build pass. An unrelated existing Code MCP skill-contract test also fails on unchanged origin/main (tracked separately in PLAT-484).

Right-panel Ask AI follow-up: 28 existing integration-prompt, Incoming email and account-permission UI checks pass; frontend type checking passes.

## Remaining

Deploy the PLAT-499 review/provisioning batch before proceeding on RTS. The PLAT-497 gateway repair is verified live. On RTS, an app administrator must complete the reviewed Google Cloud consent with the existing OAuth project's permissions. Verify the public event/callback paths, connect the mailbox with read consent, wait for watch readiness and test a real email. Live Google provisioning/delivery has not been exercised by this task. Other deployments require the updated release when requested.
