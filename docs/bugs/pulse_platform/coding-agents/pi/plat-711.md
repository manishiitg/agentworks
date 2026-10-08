[← coding-agents / pi](index.md)

# PLAT-711: Pi: deployment-supplied providers/models staged into each session

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | coding-agents |
| Area | pi |
| Summary | A deployment can supply Pi custom providers/models (PI_CLI_AGENT_TEMPLATE_DIR: models.json + thinking settings, no keys) that are staged into every session agent dir; first user Citymall's gateway. |

## What happened

Every AgentWorks Pi session runs with its own private `PI_CODING_AGENT_DIR`, so a Pi `models.json` placed in the
service HOME was never seen: a deployment could not run Pi on its own OpenAI-compatible gateway (Citymall).

## Fix

- multi-llm-provider-go af24edf (pi-cli adapter): `PI_CLI_AGENT_TEMPLATE_DIR` names a folder with `models.json` and an
  optional `settings.json` (only thinking keys). Both are validated and staged (0600) into each session agent dir
  before Pi starts; a bad template fails the launch. The template cannot hold a key: `apiKey` and credential headers
  must be `$<PROVIDER>_API_KEY`, the variable the adapter injects from the scoped provider credential (server env or
  a Pi account's underlying-provider key); `!command` values are refused. Launch-arg redaction covers any
  `<PROVIDER>_API_KEY`. Test: TestPiAgentTemplateStagedIntoSessionDirAndRefusesLiteralKeys.
- multi-llm-provider-go a7fcb3c: the template's models are in the Pi model catalog, so the app accepts them as a
  chat's model (without it a Code/Brain chat on citymall/gpt-6-luna was refused: "model is not offered for engine
  pi-cli").
- agent_go: the template's models lead the Pi model picker (first is the default) and a template provider key in the
  server environment makes the Pi server account configured (Pi's own --list-models cannot see it).
- Citymall's template (deploy/rootless-linux/products/citymall/pi-agent): gpt-6-luna first, gpt-5.6-luna second,
  thinking off mapped to `"none"` and off by default, because the gateway refuses tool calls with any other
  reasoning setting.

## Left

- Pricing and context limits of the Citymall models are unknown; the picker shows none.

## 2026-10-08

Owner: offer only gpt-6-luna. gpt-5.6-luna removed from the Citymall Pi template (models.json, settings.json).
