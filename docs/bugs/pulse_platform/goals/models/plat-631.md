[← goals / models](index.md)

# PLAT-631: Workflow model card shows execution effort instead of Builder effort and retained turns ignore effort changes

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | models |
| Summary | The workflow model card showed the High execution default beside a Medium Builder chat; effort-only changes also bypassed retained runtime refresh. |

## What happened

On Excellence, Vaibhav's Timouthy candidate-sourcing workflow follows Claude's
managed provider profile. Its Builder default is Sonnet 5.5 with Medium effort;
its High execution tier differs. The single Model card displayed the first
execution tier while the adjacent chat used Builder. A read-only process check
confirmed actual `--model claude-sonnet-5-5 --effort medium`, and the launch log
also recorded Medium. Effort was passed correctly for this managed profile;
the displayed default described the wrong role.

A second issue was found in retained workflow delivery: provider, model and
account were compared, but options (including reasoning effort) were not. An
effort-only edit could keep the existing CLI's launch flags. The continuation
request could also keep stale tab options instead of the manifest's actual
Builder options.

Code and Crew use their separate single Builder-model panel and persisted
profile reasoning setting. Existing effort selection/account preservation and
conversation-restart checks pass; this screenshot's session is a workflow, not
a Code or Crew chat. No execution-role controls were added to those products.

## Fix

- Display Builder's model and effort in the workflow's single card. Preserve
  provider defaults on mount; choosing a model/effort still applies it to every
  role with the selected account. Collapsing per-role choices confirms use of
  Builder's setting.
- Record the actual manifest model, account and options in retained turn context.
- Compare a canonical options fingerprint for warm and saved runtimes. Legacy
  snapshots reconnect once while preserving native conversation history.
- Queue runtime changes behind a running turn before closing its CLI. Check
  authority first: changed permissions still revoke the old runtime immediately.

## Validation

Frontend panel/role regressions and existing Code/Crew model-panel checks pass;
TypeScript build passes. Backend regressions drive the live-input HTTP handler
through busy queueing and idle rebuild after an effort-only change; unchanged
High effort retains the CLI. Existing permission/provider/account checks pass.
A production Builder query assembly regression checks the prepared model and
saved continuation use manifest High over stale browser Medium, with its
options fingerprint surviving saved runtime serialization. It stops before a
model request; external workspace state is mocked.

## Left

Deploy the fix to Excellence and verify health/confinement after restart.
