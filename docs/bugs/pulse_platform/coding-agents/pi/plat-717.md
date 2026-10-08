[← coding-agents / pi](index.md)

# PLAT-717: Bring your own model key: OpenRouter, NVIDIA NIM, Groq, Google AI Studio and OpenAI-compatible endpoints through Pi

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | pi |
| Summary | Guided "Use your own model key" flow (service tiles, key + Test key, live model browser with Free/price/tools/Try it, picks), entry points on Providers and in the chat Models panel, free models first in chat, a hint when a free model is busy or gone. Not deployed; no real-key call made yet. |

## What happened

Adding a Pi account asked for a free-text "Pi provider ID" (default `google`)
and a key. Nobody knew that `openrouter` was the id to type, that OpenRouter
and NVIDIA have free models, or which of them can do tool calls, and the chat
pickers showed the server's Pi catalog, not the models of the person's key.
Users asked (#agent_works) for OpenRouter, NVIDIA NIM (free, 40 req/min) and
any OpenAI-compatible endpoint.

## Fix

- Providers: a "Bring your own model key" card in the sidebar opens Pi's add
  flow; Pi's "Add a model key" is a three-step flow (`ByokSetup.tsx`):
  1. service tiles: OpenRouter (recommended), NVIDIA NIM, Groq, Google AI
     Studio, Other OpenAI-compatible, Other Pi provider (the old free-text id);
     each with one line, its free tier and a "Get a key" link;
  2. key (masked, format hint), optional name, base URL for a custom endpoint,
     **Test key** (`POST /api/byok/test-key`: OpenRouter `/key` with free-tier
     and usage, Groq/Google/custom model list, NVIDIA a 1-token request);
  3. model browser (`ByokModelBrowser.tsx`, `POST /api/byok/models`): live
     catalog, search, Free and "Recommended for agents" filters, Free badge,
     price per 1M in/out, context, tools badge, **Try it**
     (`POST /api/byok/try-model`: one chat request with one tool, never run;
     passes when the model calls it), star picks (first = default).
  Saving stores a private pi-cli account: `underlying_provider` = service,
  `allowed_models` = picks (so a turn never falls back to another service's
  model), `base_url` for a custom endpoint.
- Manage: rows show the service, "Key works · OpenRouter key · free tier ·
  $x used" / "Key rejected", menu Test key again, Models (the browser),
  Rename or replace key, Who can use it (PLAT-715 rules), Remove.
- Chat Models panel (`WorkModelsPanel.tsx`): on a key account the model cards
  are the key's picks, free first, with Free badge and price; "Browse
  <service> models" edits the picks; "Use your own model key (free models
  available)" opens the flow in a dialog and switches the chat to the new key
  (a callout when no coding agent is available). Account rows say "Your
  OpenRouter key". A turn error with a rate limit / unknown model shows "This
  model is busy, rate-limited or gone… Pick another model" with a button to
  the Models tab.
- Backend `cmd/server/byok.go`; OpenRouter catalog now carries
  `supports_tools`; NVIDIA's public catalog, filtered to chat models, marked
  free; recommended for agents = free + tools on OpenRouter, curated lists
  elsewhere; default pick on OpenRouter `nvidia/nemotron-3-super-120b-a12b:free`
  (free, tools in the live catalog 2026-10-08), on NVIDIA `z-ai/glm-5.3-flash`.
- Pi path: model ids `openrouter/<vendor>/<model>`, `nvidia/<model>`,
  `groq/<model>`, `google/<model>`, `openai-compatible/<model>`; the adapter
  passes `--provider <service> --model <rest>` and the key as
  `<SERVICE>_API_KEY` (Pi has built-in openrouter, nvidia, groq, google). An
  OpenAI-compatible account is staged into the session's Pi `models.json`
  (multi-llm-provider-go `PiCustomProvider`, key only as an env reference).
- Custom endpoints: https and a public address only (DNS-checked at connect
  for the setup calls); `AGENTWORKS_BYOK_ALLOW_PRIVATE_ENDPOINTS=1` allows a
  private one.
- Tests: `TestByokOpenRouterKeyReachesPiOnlyAndFreeFlagsReachThePicker`,
  `ByokSetup.test.tsx`, mlp `TestPiCustomProviderStagedByKeyReference`.

Live checks on 2026-10-08 (no key): Pi 1.0.4 lists 398 OpenRouter models (15
`:free`) and 21 NVIDIA ones, and accepts an unknown OpenRouter id ("Using
custom model id"); NVIDIA `/v1/models` is public and has the four requested
ids (`z-ai/glm-5.3-flash`, not `glm-5-3-flash`). On OpenRouter Kimi K3 is
paid (`moonshotai/kimi-k3`, $0.58/$12.30 per 1M); no `kimi-k3-256k` and no free
Kimi.

## Left

- No call with a real key yet (no key on hand): after deploy, one user with an
  OpenRouter key (and one with NVIDIA) adds it, runs Test key and Try it, and
  sends a chat turn with a tool call on the default free model.
- Pi itself reaches a custom endpoint without the private-address check (only
  the setup calls are guarded, and the URL is checked when saved).
- Not deployed.
