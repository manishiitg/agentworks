# Unified Crew local preview

PLAT-746-265. Experimental first increment, kept off main. This is not the completed Crew consolidation or a data migration.

## Start and open

From this worktree, run `./scripts/run-crew-lab.sh`. Open <http://127.0.0.1:53733>.

The launcher uses API 20743, workspace 20744 and gateway 20745. It refuses occupied ports. Vite stays on for frontend edits; Electron is not launched. Stop the launcher with Ctrl+C when finished.

State and synthetic workspaces live under `.local/agentworks-instances/unified-crew/` in this worktree. The pinned Python runtime lives separately under `.local/crew-lab-runtime/`. The normal app's data, accounts, connections and ports are not copied or changed. No company server is contacted by the sample work. The isolated scheduler is enabled because manual program invocations need its durable run store; no clock schedules are created by the launcher.

## Try this increment

Open a Crew's menu and choose **Workflows & programs**. Add a workflow or program and open it. Open a definition inside its Crew with **Steps · Runs · Settings**. The existing canvas, source, logs, Inspect and cost panels remain in use; opening them does not switch the Crew chat. Crew-owned definitions no longer get duplicate standalone sidebar rows.

The original preview instance's prepared **Crew Lab** contains a greeting workflow (two Python steps, no model calls, variables or groups), a greeting program (native DBOS Relay), and a second workflow created through the browser.

## Implemented

- A trusted record outside editable workspace files binds each executable to its parent Crew. IDs survive renaming.
- Live Crew ownership and sharing decide access. Editable child ownership/team fields cannot grant access or choose another execution account. Crew work remains on its main owner's account.
- Missing variables/groups means one unnamed invocation in the existing runner. No synthetic default group is saved. Existing named sets and their run paths remain intact.
- Unknown sets, malformed inputs and read failures are errors. Explicit empty selection and all-disabled sets never fall back to running all work. An explicit empty input-set selection is refused before schedule loading and execution.
- Crew-owned schedules, functions and webhooks save a target chat (Main by default), reuse its durable conversation, and record invocation start/outcome there. Each run still has its own execution identity and checkpoint. Scripts and native programs do not need an extra model turn.
- Builders can select an existing Crew chat in schedule/function settings. A missing chat refuses execution; it never falls back to another chat. Run users cannot list the owner's private chat names.
- Knowledge-source authorization uses the Crew's live audience on both sides. Scoped channel grants retain their matching-target checks, and corrupt ownership records never grant account-tier fallback access.
- Local launch has independent gateway/Vault state and uses the repository's pinned bridge dependency.

## Still unfinished

- Split definition/code/run/data locations and the final shared Crew folder structure.
- Direct-script publishing and one shared callable input contract.
- Final input-selection API cleanup, full direct form authoring, and remaining global-monitor/deep-link navigation into a child inside its Crew.
- Targets/measurement integration, conversion tooling, existing-data conversion and old-path removal.
- Linux slot/Landlock checks for the future split-root layout, full permissions/agentic scenarios and live rollout.

Do not use this preview as proof that the complete consolidation is ready to merge or migrate. No merge, deployment or live-data conversion is part of this preview.

## Updated review branch

The review branch includes the latest main available at its publication and fixes the five initial Codex findings: schedule execution owner overrides, binding-error access fallback, knowledge-source audiences, scoped channel access and empty schedule selections. A follow-up review also corrected private chat choices in automation responses.

Focused checks exercise the actual access/channel endpoints and store projections. A real HTTP workspace/native DBOS recovery check creates a Crew, invokes its saved program, preserves the checkpoint after a process crash, records the result in Main, reuses a selected side chat and refuses a missing target. TypeScript and the focused function/schedule UI checks pass. These checks use synthetic temporary data, not a person's running app or provider account.

Updating the review branch does not update an already-running preview. To start another isolated browser instance from this checkout, choose free ports, for example:

```sh
./scripts/run-crew-lab.sh --instance crew-review \
  --agent-port 22743 --workspace-port 22744 --gateway-port 22745 \
  --frontend-port 55733 --electron-debug-port 22233
```

The existing isolated instance and the normal app may remain running. This branch remains review-only; existing Goal/Relay records have not been converted. The saved-chat rule applies to executables bound to a Crew in this refactor. Existing standalone products retain their current behavior until conversion.


## Embedded editor and agent reuse

The review branch now keeps editing inside the Crew. Steps reuses the existing canvas and Python program view. Runs shows the selected workflow run's logs, files, exact step inspection receipts and per-run costs. Programs reuse the existing release/run selector, so changing from draft to a published version changes the workspace used by every detail panel. Settings reuses schedules, functions and webhooks; their saved chat targets remain authoritative. Edit/Run in chat prepares a request in the existing Crew chat. Its owner-only file tool lists the Crew’s children and saved agents, and reads/writes their definition files through the existing workspace writers. It cannot edit manifests, run output or another Crew’s children. Plans use the existing structural validator. Whole-run chat calls use the existing workflow trigger with automatic inputs for Crew children, so no default group is required.

Selecting a Crew agent on an agent step reuses its saved role, Crew instructions, model/account, apps and skills. Explicit step overrides stay in effect and existing workflow tool/folder limits still apply. The executor uses its existing run-scoped sequence; no ordinary chat messages, native handle or session identity are imported. A reference to another Crew, even one with the same owner, is refused. The configuration is copied for each new sequence and retained for route reentry within that run.

Run users do not mount this editor. The server's Crew access response gates the screen, and all child reads/writes still use normal server authorization. Parent metadata used for navigation is derived from the private binding, not editable workflow fields.

Focused checks cover preserving the active Crew and unrelated workflow state, selecting the exact run/step, per-run costs without workspace-total fallback, owner/foreign access, saved-specialist updates, and configuration/history separation. Real coding-agent calls and Linux split-root behavior remain unverified. The separate preview uses synthetic scripts and the real workspace/DBOS path.

The preview’s whole-plan file writes do not yet reuse the external Builder’s revision/conflict and plan-changelog contract. Concurrent edits can overwrite each other. This remains a recorded limitation, rather than a second revision system.
