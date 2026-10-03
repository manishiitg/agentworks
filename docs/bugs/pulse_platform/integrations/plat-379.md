# PLAT-379 — Workflow-only notifications and actionable Gmail receiving setup

Status: implemented and locally validated; deployment pending.

## Problem

`notify_user` was exposed to Relay Builder and SparkQuill parent/child profiles,
and Relay runs inherited it from the shared workflow runner. SparkQuill prompts
and its isolated check-in asked agents to send notifications. The user requires
this platform tool to be available only in workflows.

Incoming Gmail displayed an unexplained administrator requirement. Builders saw
`configured: false` and an empty OAuth client list without deployment-specific
instructions, even when Google sign-in already worked.

## Change

- Admit `notify_user` only for saved workflows, across chat registration and
  execution pools. Product declarations cannot grant it. Relay runner catalogs,
  executors and categories exclude it. Other platform tools and Gmail trigger
  replies keep their existing behavior.
- Remove Relay/SparkQuill declarations and SparkQuill's attached notify skill.
  Check-ins save a summary in their isolated history and Progress tab.
- Gmail status returns credential-free `setup.admin_setup`, public event URL,
  required access and the Google Cloud/server checklist. Shared read-only panel
  explains sign-in versus receiving and offers an expandable setup checklist.
  Builder guidance explains missing topic mapping and permissions in plain language.
- Infrastructure remains operator-managed; account consent and trigger/rule
  configuration remain Builder-managed. No automatic cloud provisioning/deploy.

## Verification

Registration authority, factory bypass, runner definition/executor/category,
manifest kind, product declarations, disabled Gmail Builder response, read-only
UI/help and frontend types. No live Google mailbox test or RTS deployment.

All product package tests, scoped server regression/race tests, 13 Gmail UI
tests, frontend type checking and the full production build passed.
