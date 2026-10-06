[← platform / security-sandbox](index.md)

# PLAT-399 — Script output directory is not writable by its user slot

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | fixed on main; deployment pending. |

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-03 |
| Owner | security-sandbox |

## Evidence and cause

On Excellence, Relay test ran Extract PDF text successfully but Extract invoice
fields failed resolving `steps.extract_pdf_text.output.text`: its result.json
was missing. The earlier probe reported Permission denied creating result.json
and output.json in STEP_OUTPUT_DIR. The directory and its parents were owned by
agents:slotshared with mode 2750, so the script's slot could traverse/read but
could not write. The inspected release was agents-e7db4f50-20261003193112.

The same probe's `python3 -m pip list --format=json` succeeded and enumerated
75 packages. PDF extraction later used the available pdftotext executable.
This is not a pip failure. Packages selected for code layout use the existing
WORKFLOW_CODE_DEPS/PYTHONPATH contract; no system-wide installation is needed.

The shared /api/execute precreation used MkdirAll(0755), which cannot add group
write on an existing service-created directory. Folder Guard grants do not
supersede Unix permissions. Adding context_output only declared the file name;
it never collected stdout or fixed its folder permissions. The builder had
stopped after this failed handoff; investigation did not resume or edit it.

## Implemented

- For a slotted shell, prepare only validated workspace write directories as
  group-writable, setgid and closed to others. Existing service-owned targets
  are repaired. Service-owned ancestors get traversal only, not write access.
- Use open directory descriptors and NOFOLLOW for each path component while
  changing modes. Escaped paths, host grants, read-only targets, unrelated
  directories, slot-owned directories and the workspace root are unchanged.
- Fail admission if preparation fails. Local/non-slot commands keep their
  previous modes. There is no Relay-specific runner or extra sandbox grant.
- Relay prompt/skill require scripts to write result.json under STEP_OUTPUT_DIR
  for JSON handoff and to raise output-write errors. Stdout is a log and
  context_output declares a saved file; it cannot create or collect one.

## Verification

Local workspace path-boundary regressions passed. Opt-in Linux runner and
permission tests exercise a throwaway slot folder, never the live Relay:
output creation, pip listing, parent traversal and denial of an ungranted file.
The three Linux tests passed through Excellence's shipped launcher: the shell
ran as slot03, wrote/read valid result.json, completed pip listing and refused
the ungranted file. A stricter ancestor-mode fixture also passed with the fix;
the old runner failed exit 126 before execution with a slot permission denial.
The isolated integration never changed live Relay files or installed packages.
Relay product tests and frontend TypeScript/16 regressions also passed. The
workspace build, frontend type check and commit secret scan passed.

## Remaining

Deploy the workspace service and product guidance, then retry the full invoice
Relay with its sample INPUT. A full model-driven invoice run is not yet verified
against the new service; the deployed service was not restarted or modified.

## Register notes

[PLAT-399](plat-399.md), fixed on main; deployment
pending. Shared shell preparation makes granted output directories writable by
slots. Relay guidance requires file outputs; pip listing itself succeeded.
