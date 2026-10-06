[← goals / step-execution](index.md)

# PLAT-005 — `get_api_spec` does not honor its multi-name input

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | API bridge tool-spec lookup |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** API bridge tool-spec lookup
- **Source finding:** `HARNESS-GET-API-SPEC-ARRAY`
- **Source database:** `Workflow/rtslatency/db/db.sqlite`
- **Problem:** an array of valid tool names is coerced into one literal unknown
  name instead of returning several specs.
- **Impact:** an agent concludes that working tools do not exist and can publish
  that false diagnosis downstream.
- **Resolution:** fixed in mcpagent commit `ea60eb2` ("Stop get_api_spec failing
  on shape and routing"). `tool_name` accepts one string, a decoded JSON array,
  or a coding-CLI-serialized JSON-array string; canonical tool names are sorted,
  resolved, and authorized independently of the compatibility-only
  `server_name` field.
- **Verification (2026-08-03):** array, serialized-array, mixed known/unknown,
  custom/MCP routing, and unavailable-server tests pass. The RTS finding is
  historical evidence and should move to platform reverify on the rebuilt
  binary.
- **Current workaround:** one lookup call per tool name.
- **Acceptance:** string and string-array inputs return the same canonical specs;
  mixed known/unknown input identifies only the unknown names without hiding
  the known results.
