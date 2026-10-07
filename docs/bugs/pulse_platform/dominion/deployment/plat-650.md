[← dominion / deployment](index.md)

# PLAT-650: Dominion service processes miss provisioned slot groups and block Python Relay runners

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | dominion |
| Area | deployment |
| Summary | Refresh the stale systemd user manager after first slot provisioning; preserve existing slot confinement and document the required running-process check. |

## What happened

On 2026-10-07 the Relay graph smoke test builder created a ticket-triage Python
Relay. Run 649c18ad-348d-5f8e-a176-f2a285b8a4e0 failed before user code or agent calls:
Python could not read runs/iteration-2-hook/.relay_ipc/runner.py.

The account database correctly lists dominion in dm01 through dm05 and dmshared.
But /proc/<pid>/status showed only primary group 987 for the user manager, agent
and workspace. Individual service restarts inherited the stale manager groups.
Linux cleared the new run folder's setgid bit because the creating service did
not hold its inherited dmshared group. Its IPC directory and files consequently
belonged to dominion:dominion (0770/0640), unreadable to the owner's dm01 slot.
The earlier starter folder had been repaired by the root provisioning pass, so
it worked and concealed the stale process membership.

## Fix

- Confirm the backend is idle (zero active sessions and in-flight requests),
  then as root refresh user@995.service. Start/verify Dominion's already-enabled
  agent, workspace, Vault and gateway services on the existing release.
- Verify actual service process groups contain 1076 through 1081 as configured.
- Correct provision-slots.sh guidance: restarting individual systemd user
  services cannot refresh the manager's groups. Report the operator command and
  require waiting for active work. Provisioning does not automatically restart
  services or interrupt active runs.
- Document running-process verification in docs/security/per_user_linux_accounts.md.

## Security review

Reviewed the per-user account design, root-owned slot table/launcher and fixed
sudo command list, workspace shell account selection and folder-guard requirement,
app-private grant filtering, and Python Relay's existing sandbox/session guard.
This is a correction to the service account's already provisioned groups. No
slot assignments, Unix modes, ACLs, sudoers, sandbox policies or credentials were
changed; user code still runs as dm01 via the existing launcher. Private state,
Vault state and .env remain 0700/0700/0600; the slot table remains root:dominion 0640.
Dominion remains opt-in: users assigned a slot run as it; unassigned users retain
the platform's existing opt-in behavior.

## Verification

- All four services active; agent health healthy and idle after manager refresh.
- Live slot self-test: 32 passed, zero failed, six skipped (no Crew/Code project
  fixtures for the two assigned slots). Denials cover users' folders, app state,
  workflow boundaries, environment secrets and tmux socket exposure.
- Through the authenticated internal workspace API, create a throwaway run/IPC
  directory and runner using the normal folder/file handlers. Execute the
  unchanged ticket-triage relay.py through the production shell handler as dm01
  with the same Relay read/write/blocked folder grants and an empty ticket batch.
  The generated runner inherited dmshared, Python completed and saved exactly
  {"tickets": [], "digest": "No tickets supplied.", "counts": {}} plus a completed
  trace with no agent calls. Remove the throwaway fixture afterward.
- Source changes only correct operator guidance; shell syntax and generated
  ticket validation checked in the owned worktree.

## Left

The original failed run remains a historical failure. New test invocations use
new run folders. The live check verifies the Python runner and result path; the
nonempty triage loop, custom tool, branch and model calls still need their normal
product test rerun. The deployed binary revision remains c4034de369bdb00d3a81351a7815f9b3aa948c0a.
