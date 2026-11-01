# Unified Crew local preview

PLAT-746-265. Experimental first increment, kept off main. This is not the completed Crew consolidation or a data migration.

## Start and open

From this worktree, run `./scripts/run-crew-lab.sh`. Open <http://127.0.0.1:53733>.

The launcher uses API 20743, workspace 20744 and gateway 20745. It refuses occupied ports. Vite stays on for frontend edits; Electron is not launched. Stop the launcher with Ctrl+C when finished.

State and synthetic workspaces live under `.local/agentworks-instances/unified-crew/` in this worktree. The pinned Python runtime lives separately under `.local/crew-lab-runtime/`. The normal app's data, accounts, connections and ports are not copied or changed. No company server is contacted by the sample work. The isolated scheduler is enabled because manual program invocations need its durable run store; no clock schedules are created by the launcher.

## Try this increment

Open a Crew's menu and choose **Workflows & programs**. Add a workflow or program and open it. The existing workflow and Relay editors/runners handle editing and execution. During this increment they still open their existing product screens and may appear in the sidebar.

The prepared local **Crew Lab** contains a greeting workflow (two Python steps, no model calls, variables or groups), a greeting program (native DBOS Relay), and a second workflow created through the browser.

## Implemented

- A trusted record outside editable workspace files binds each executable to its parent Crew. IDs survive renaming.
- Live Crew ownership and sharing decide access. Editable child ownership/team fields cannot grant access or choose another execution account. Crew work remains on its main owner's account.
- Missing variables/groups means one unnamed invocation in the existing runner. No synthetic default group is saved. Existing named sets and their run paths remain intact.
- Unknown sets, malformed inputs and read failures are errors. Explicit empty selection and all-disabled sets never fall back to running all work. Empty-selection reporting still uses the runner's existing error path.
- Local launch has independent gateway/Vault state and uses the repository's pinned bridge dependency.

## Still unfinished

- Split definition/code/run/data locations and the final shared Crew folder structure.
- Moving the complete editor/run experience inside the Crew, removing standalone child rows and shells.
- Referencing Crew conversational agents in workflows; direct-script publishing and one shared callable input contract.
- Full schedule/function/UI parity for optional inputs; admission-time empty-selection feedback versus skipped receipts.
- Targets/measurement integration, conversion tooling, existing-data conversion and old-path removal.
- Linux slot/Landlock checks for the future split-root layout, full permissions/agentic scenarios and live rollout.

Do not use this preview as proof that the complete consolidation is ready to merge or migrate. No merge, deployment or live-data conversion is part of this preview.
