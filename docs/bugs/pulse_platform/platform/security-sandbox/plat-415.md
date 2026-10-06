[← platform / security-sandbox](index.md)

# PLAT-415 — Structured Python outputs through the shared runner

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | fixed on main; deployment pending. |

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## User decision and implementation

Users pass structured values between Relay nodes without managing files.
Agent responses already persist automatically. Python scripts now use the
shared built-in `from agentworks_output import set_output` helper, calling
set_output(value). The helper serializes strict JSON, writes a temporary file
in STEP_OUTPUT_DIR, and atomically replaces result.json. Serialization/write
errors raise; the previous output survives failed serialization. Slot-created
results use group-readable mode 0660 so the platform can collect them.

The existing result.json handoff and context_output declaration remain; no
new executor or stdout collector was introduced. Multiple successful calls
replace the output. Old scripts remain compatible. Logs and recovery artifacts
continue to persist; this does not add node-boundary recovery.

The sandbox supplies the helper. For slot commands it is staged in a unique
service-owned directory in the slot run area, readable but not writable by the
slot. The setgid directory retains the shared group. Its read grant and
PYTHONPATH are added before serializing the slot request; existing workflow
import paths are preserved. Scratch cleanup removes it after the command.
Non-slot commands use the existing private scratch Python helper path.

Relay manifest-owned builder guidance and learn_code_flow describe the API.

## Verification

- Helper subprocess tests: nested/unicode JSON, replacements, mode 0660,
  NaN/Infinity/nonserializable rejection without overwriting prior output,
  missing/relative output directory rejection, write failures propagate.
- Real Excellence slot test through /api/execute: helper import, existing
  PYTHONPATH import, output persistence readable by service, helper write
  denial, pip listing and denial of an ungranted sibling file.
- Shared slot directory permissions regression and local environment tests.
- Builder prompt template renders literal input/output references correctly.

Tests use throwaway paths only; no live Relay, service, credential or package
installation was changed. Test binaries and remote fixtures were removed.

## Remaining

Deploy the shared workspace runner and embedded Relay guidance through the
normal release. PLAT-411's separate agent tool errors remain open.

## Register notes

[PLAT-415](plat-415.md), fixed on main;
deployment pending. Shared set_output(value) helper persists structured JSON
for downstream references without user-managed files; slot runtime imports
and artifact permissions are verified.
