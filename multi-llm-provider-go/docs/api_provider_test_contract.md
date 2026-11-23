# API Provider Test Contract

This document defines the normative testing contract for **API-based LLM
provider adapters** in `multi-llm-provider-go` — i.e. providers that hit
a remote HTTP endpoint with an API key, as opposed to the
locally-launched CLI coding agents (which have their own contract at
[`coding_sdk_tmux_contract.md`](./coding_sdk_tmux_contract.md)).

Covered providers (the package directories under `pkg/adapters/`):

- `anthropic` — Claude via Messages API
- `openai` — GPT family
- `vertex` — Google Vertex AI (Gemini)
- `bedrock` — AWS Bedrock (Claude / Llama / etc.)
- `azure` — Azure OpenAI
- `zai` — Z.AI (GLM)
- `kimi` — Moonshot (direct Kimi API)
- `minimax` — MiniMax
- `elevenlabs` — TTS / voice
- `deepgram` — STT

Each provider exposes its own Go adapter under `pkg/adapters/<name>/`.

## Goals

The contract serves three audiences:

1. **Adapter authors** — what a new adapter must implement and prove
   before it's "done."
2. **Reviewers** — a checklist when auditing an existing adapter for
   regressions or modernization gaps.
3. **Operators** — a stable, machine-readable list of what each
   provider supports, so capability decisions (which provider to fall
   back to, which to use for thinking, which to use for vision) can be
   pinned to documented behavior instead of inference.

The mechanical source of truth lives in **the adapter's own real-API
test file**. Every supported area below must have a corresponding test
in `pkg/adapters/<name>/<name>_real_test.go` (or equivalent) that hits
the live API with the documented env-gated key. A provider is "P0
complete" when every P0 row in the matrix is exercised by at least one
real-API test.

## Test File Layout

Every API provider adapter must follow this file convention:

```
pkg/adapters/<provider>/
├── <provider>_adapter.go             # adapter implementation
├── <provider>_models.go              # model metadata + tier resolution
├── <provider>_adapter_test.go        # pure unit tests (no network)
├── <provider>_real_test.go           # real-API e2e tests, env-gated
└── mock_logger_test.go               # MockLogger used by real tests
```

Real-API tests live in `*_real_test.go` and **must** be gated by two
environment variables:

- `RUN_<PROVIDER>_REAL_E2E=1` — opt-in flag so default `go test ./...`
  runs do not burn budget on a passive API key.
- `<PROVIDER>_API_KEY=...` — the actual credential.

Tests skip cleanly (via `t.Skip`) when either is missing. The
gate-helper pattern is:

```go
func require<Provider>RealE2E(t *testing.T) (apiKey, model string) {
    t.Helper()
    if os.Getenv("RUN_<PROVIDER>_REAL_E2E") == "" {
        t.Skip("set RUN_<PROVIDER>_REAL_E2E=1 to run real <Provider> API tests")
    }
    apiKey = strings.TrimSpace(os.Getenv("<PROVIDER>_API_KEY"))
    if apiKey == "" {
        t.Skip("set <PROVIDER>_API_KEY to run real <Provider> API tests")
    }
    model = strings.TrimSpace(os.Getenv("<PROVIDER>_REAL_E2E_MODEL"))
    if model == "" {
        model = "<adapter default fast/cheap model>"
    }
    return apiKey, model
}
```

The model env override is required so a single key can validate
multiple tiers (e.g. Haiku 4.5 by default, Opus 4.7 on demand) without
rebuilds.

## Normative Test Areas

Each area below has an **assertion pattern** (what the test must prove)
and a **tier** (P0 = must have, P1 = should have, P2 = optional). Tests
should be small and atomic: one area per test function, one assertion
group per area, no shared fixture state.

### P0 — Core message flow

#### 1. Plain-text generation

- **Test**: `Test<Provider>RealPlainText`
- **Sends**: one user message asking for a short, deterministic reply.
- **Asserts**: `resp.Choices[0].Content` is non-empty and contains the
  expected token.
- **Why**: dead-simplest smoke that the API key works and the request
  shape compiles.

#### 2. Streaming text deltas

- **Test**: `Test<Provider>RealStreaming`
- **Sends**: a `StreamChan` and a prompt that produces enough tokens to
  emit multiple deltas.
- **Asserts**: at least two `StreamChunkTypeContent` chunks arrive
  before the final response, and the joined deltas match
  `Choices[0].Content`.
- **Why**: streaming is the normal product path; non-streaming smoke
  tests can pass while streaming silently drops chunks.

#### 3. Multi-turn conversation

- **Test**: `Test<Provider>RealMultiTurn`
- **Sends**: three messages — user → assistant (previous reply)
  → user (follow-up that references the previous reply).
- **Asserts**: the final reply demonstrates awareness of the previous
  turn (e.g. echoes back a token the assistant introduced).
- **Why**: catches conversation-flattening bugs where the adapter
  drops history.

#### 4. System prompt

- **Test**: `Test<Provider>RealSystemPrompt`
- **Sends**: a system message that demands a specific output format
  (e.g. "reply only with the word OK"), plus a user prompt.
- **Asserts**: the system instruction is honored.
- **Why**: many adapters incorrectly route system messages into the
  user content; this test fails fast when they do.

#### 5. Token usage in response

- **Test**: `Test<Provider>RealTokenUsage`
- **Sends**: any reasonable prompt.
- **Asserts**: `resp.Usage.InputTokens > 0`, `OutputTokens > 0`, and
  `Choices[0].GenerationInfo.InputTokens` / `OutputTokens` match.
- **Why**: cost tracking depends on these fields. Adapters that forget
  to populate them are silently invisible to billing.

#### 6. Cancellation

- **Test**: `Test<Provider>RealCancellation`
- **Sends**: a long-output prompt, then cancels the context after
  ~200ms.
- **Asserts**: `GenerateContent` returns with a `context.Canceled` (or
  wrapped) error within a small bounded time after cancel.
- **Why**: leaked goroutines, hung HTTP requests, and unclosed
  streaming channels all show up here.

#### 7. Tool definitions and selection

- **Test**: `Test<Provider>RealToolCall`
- **Sends**: a prompt + a single tool with a meaningful **name**,
  **description**, and **input_schema**.
- **Asserts**: response contains `Choices[0].ToolCalls[0]` with the
  expected `Name` and parsed arguments.
- **Why**: name + schema alone are not enough; tools without
  descriptions degrade quality silently (see Anthropic adapter Nov-2025
  regression).

#### 8. Tool description fidelity

- **Test**: `Test<Provider>RealToolDescriptionInfluencesSelection`
- **Sends**: two tools with **deliberately confusable names** but
  **very different descriptions** (e.g. `lookup_alpha` = time tool,
  `lookup_beta` = weather tool); user asks a weather question.
- **Asserts**: the model picks `lookup_beta` based on the description.
- **Why**: this is the specific regression test that protects against
  the "tool description silently dropped" class of bug.

#### 9. Tool choice modes

- **Test**: `Test<Provider>RealToolChoiceModes`
- **Sends**: same tool set, varying `ToolChoice` between `auto`,
  `none`, and `required`.
- **Asserts**: `auto` may emit a tool call, `none` emits text only,
  `required` always emits a tool call.
- **Why**: providers diverge here (Anthropic supports `tool` specific
  selection; OpenAI uses `tool_choice: {type:"function",name:...}`);
  the adapter must hide that.

### P1 — Sampling controls

#### 10. Stop sequences

- **Test**: `Test<Provider>RealStopSequences`
- **Sends**: a prompt that would naturally produce a known sequence
  (e.g. "ITEM-1, ITEM-2, ITEM-3, ...") with `StopSequences: ["ITEM-3"]`.
- **Asserts**: response contains ITEM-1 and ITEM-2 but **does NOT**
  contain ITEM-3.
- **Skips when**: provider does not natively support stop sequences.

#### 11. top_p

- **Test**: `Test<Provider>RealTopPDoesNotError`
- **Sends**: a prompt + `WithTopP(0.9)`.
- **Asserts**: the request succeeds (we can't deterministically check
  distribution from a single call).

#### 12. top_k

- **Test**: `Test<Provider>RealTopKDoesNotError`
- **Sends**: a prompt + `WithTopK(40)`.
- **Asserts**: the request succeeds.
- **Skips when**: provider rejects top_k (OpenAI Chat Completions does).

### P1 — Content types

#### 13. Image input (base64)

- **Test**: `Test<Provider>RealImageInputBase64`
- **Sends**: a small embedded test image (PNG, < 1KB) and a question
  about its content.
- **Asserts**: response mentions a feature of the image (e.g. its
  primary color).

#### 14. Image input (URL)

- **Test**: `Test<Provider>RealImageInputURL`
- **Sends**: a public stable test image URL.
- **Asserts**: same as #13.
- **Skips when**: provider only accepts inline data.

#### 15. Document / PDF input

- **Test**: `Test<Provider>RealPDFInput`
- **Sends**: a base64-encoded minimal PDF with a known marker, plus a
  question asking for the marker.
- **Asserts**: response contains the marker, OR (loose form) response
  acknowledges the document and does not say "no document attached".
- **Skips when**: provider does not support document input
  (most don't yet).

#### 16. Tool result with image

- **Test**: `Test<Provider>RealToolResultImage`
- **Sends**: assistant tool_use → user tool_result whose content
  includes an image.
- **Asserts**: on the next turn, the model can describe the image from
  the tool result.
- **Skips when**: provider does not support image content in
  tool_result blocks (Anthropic 3.5+ supports it; OpenAI does not).

### P1 — Advanced reasoning

#### 17. Extended thinking / reasoning effort

- **Test**: `Test<Provider>RealExtendedThinking`
- **Sends**: a multi-step reasoning prompt with `WithThinkingBudget`
  (Anthropic), `WithReasoningEffort("high")` (OpenAI gpt-5.1), or
  equivalent.
- **Asserts**: response contains a thinking trace (in `GenerationInfo`
  for providers that expose it) AND a final answer that reflects the
  reasoning.
- **Skips when**: the provider's model line doesn't support extended
  thinking.

#### 18. Interleaved thinking with tools

- **Test**: `Test<Provider>RealInterleavedThinkingWithTools`
- **Sends**: a prompt that requires both reasoning and a tool call,
  with thinking enabled AND a tool declared.
- **Asserts**: the model emits a tool_use, the response includes a
  thinking trace, and the request succeeds (proves the provider's
  thinking+tools beta/flag was correctly toggled).

#### 19. Prompt caching effectiveness

- **Test**: `Test<Provider>RealPromptCachingCacheRead`
- **Sends**: a large system prompt (>2KB) + cache marker, twice
  back-to-back.
- **Asserts**: on the **second** call,
  `GenerationInfo.Additional["cache_read_input_tokens"] > 0`.
- **Skips when**: provider does not expose cache_read metrics
  (some still don't).

### P1 — Structured output

#### 20. JSON mode

- **Test**: `Test<Provider>RealJSONMode`
- **Sends**: prompt + `JSONMode: true`.
- **Asserts**: response content parses as valid JSON.

#### 21. JSON Schema strict mode

- **Test**: `Test<Provider>RealJSONSchemaStrict`
- **Sends**: prompt + a `JSONSchemaConfig{Strict: true}` with a
  schema requiring specific keys.
- **Asserts**: response parses, schema-required keys are present,
  no extra top-level keys appear when `Strict: true`.
- **Skips when**: provider does not support JSON Schema (Anthropic
  uses the "tool_use forced" workaround; flag it explicitly).

### P2 — Operational hardening

#### 22. Auth-failure classification

- **Test**: `Test<Provider>RealAuthFailureClassified`
- **Sends**: any prompt with a deliberately wrong API key.
- **Asserts**: error is classified as auth (the provider's standard
  shape) and the user-facing message does not leak the bad key.
- **Why**: a clean auth error is the difference between "user knows to
  re-paste their key" and "user files a support ticket."

#### 23. Rate-limit classification

- **Test**: `Test<Provider>RealRateLimitClassified` (manual / on-demand)
- **Sends**: rapid back-to-back requests until a 429 is observed.
- **Asserts**: 429 is wrapped as a rate-limit error type.
- **Note**: hard to make CI-deterministic; usually run manually.

#### 24. Beta-header / capability flag composition

- **Test**: `Test<Provider>BetaHeadersCompose` (unit, not real-API)
- **Asserts**: when a request enables both feature A and feature B that
  require different beta headers, both tokens appear in the
  composed `<provider>-beta` header, deduped, in stable order.
- **Skips when**: provider has no beta-flag concept.

## Test Coverage Matrix

Status as of 2026-05-18:

| Area | anthropic | openai | vertex | bedrock | azure | zai | kimi | minimax | elevenlabs | deepgram |
|---|---|---|---|---|---|---|---|---|---|---|
| 1. Plain text | ✅ (cli-smoke) | ✅ **(go test)** | ✅ **(go test)** | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ⚠ | ✅ (cli-smoke) | n/a | n/a |
| 2. Streaming | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ❌ | ⚠ | ✅ (cli-smoke) | n/a | n/a |
| 3. Multi-turn | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ❌ | ⚠ | ❌ | n/a | n/a |
| 4. System prompt | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ **(go test)** | ✅ (cli-smoke) | ✅ (cli-smoke) | ❌ | ⚠ | ❌ | n/a | n/a |
| 5. Token usage | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ❌ | ⚠ | ✅ (cli-smoke) | n/a | n/a |
| 6. Cancellation | ✅ (cli-smoke) | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 7. Tool call | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ⚠ | ✅ (cli-smoke) | n/a | n/a |
| 8. Tool description fidelity | ✅ **(go test)** | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 9. Tool choice modes | ✅ **(go test)** | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 10. Stop sequences | ✅ **(go test)** | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 11. top_p | ✅ **(go test)** | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 12. top_k | ✅ **(go test)** | n/a | ✅ **(go test)** | ❌ | n/a | ❌ | ❌ | ❌ | n/a | n/a |
| 13. Image base64 | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ❌ | ❌ | n/a | n/a |
| 14. Image URL | ⚠ | ⚠ | ⚠ | ⚠ | ⚠ | ⚠ | ❌ | ❌ | n/a | n/a |
| 15. PDF / document | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 16. Tool result image | ⚠ (unit only) | n/a | ⚠ (unit only) | ⚠ (unit only) | n/a | ❌ | ❌ | n/a | n/a | n/a |
| 17. Extended thinking / reasoning | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a | n/a |
| 18. Interleaved thinking+tools | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | n/a | ❌ | n/a | n/a | n/a |
| 19. Prompt caching cache_read | ✅ **(go test)** | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | n/a | n/a | n/a | n/a | n/a |
| 20. JSON mode | ✅ (cli-smoke) | ✅ **(go test)** | ✅ **(go test)** | ✅ (cli-smoke) | ✅ (cli-smoke) | ✅ (cli-smoke) | ❌ | ✅ (cli-smoke) | n/a | n/a |
| 21. JSON Schema strict | ✅ **(go test, via forced tool)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | n/a | n/a |
| 22. Auth failure classified | ✅ **(go test)** | ✅ **(go test)** | ✅ **(go test)** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| 23. Rate-limit classified | ✅ **(go test, self-skips on miss)** | ✅ **(go test, self-skips on miss)** | ✅ **(go test, self-skips on miss)** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| 24. Beta headers compose | ✅ **(go test, unit)** | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a |

Legend:

- ✅ **(go test)** — covered by an env-gated `*_real_test.go` test that
  `go test` discovers and runs.
- ✅ (cli-smoke) — covered by a manual cobra command under
  `internal/testing/commands/<provider>/` runnable as
  `./bin/llm-test <name>`. Counts as coverage but does not run in
  `go test ./...`.
- ⚠ — partially covered (unit-only, or a smoke test exists but the
  assertion is too loose to count as a regression test).
- ❌ — no coverage.
- n/a — the provider does not support the feature and is not expected
  to.

## Convention: migrating cli-smoke → go test

The legacy `internal/testing/commands/<provider>/*-test.go` files are
cobra CLI commands, not Go tests. They are useful for ad-hoc debugging
but do not get exercised by `go test ./...` so CI cannot enforce them
and IDE auto-runs cannot catch regressions.

When adding new coverage, prefer the `*_real_test.go` form. When
modernizing existing coverage, **convert one area at a time** — leave
the cobra command in place until the equivalent `go test` lands, then
delete the cobra command in the same commit that promotes the go test.

The Anthropic adapter (Nov 2025) is the reference for the modernized
shape; cribbing the structure of `pkg/adapters/anthropic/anthropic_real_test.go`
is the fastest path for a new adapter author.

## Required env vars

| Provider | Run gate | Key env | Default model env |
|---|---|---|---|
| Anthropic | `RUN_ANTHROPIC_REAL_E2E=1` | `ANTHROPIC_API_KEY` | `ANTHROPIC_REAL_E2E_MODEL` (default `claude-haiku-4-5`) |
| OpenAI | `RUN_OPENAI_REAL_E2E=1` | `OPENAI_API_KEY` | `OPENAI_REAL_E2E_MODEL` (default `gpt-5.1-nano`) |
| Vertex | `RUN_VERTEX_REAL_E2E=1` | `GEMINI_API_KEY` or `VERTEX_API_KEY` or `GOOGLE_API_KEY` | `VERTEX_REAL_E2E_MODEL` (default `gemini-3.5-flash-lite`) |
| Bedrock | `RUN_BEDROCK_REAL_E2E=1` | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` + `AWS_REGION` | `BEDROCK_REAL_E2E_MODEL` |
| Azure | `RUN_AZURE_REAL_E2E=1` | `AZURE_AI_API_KEY` + `AZURE_AI_ENDPOINT` | `AZURE_REAL_E2E_MODEL` |
| Z.AI | `RUN_ZAI_REAL_E2E=1` | `ZAI_API_KEY` | `ZAI_REAL_E2E_MODEL` (default `glm-4.6`) |
| Kimi | `RUN_KIMI_REAL_E2E=1` | `KIMI_API_KEY` or `MOONSHOT_API_KEY` | `KIMI_REAL_E2E_MODEL` |
| MiniMax | `RUN_MINIMAX_REAL_E2E=1` | `MINIMAX_API_KEY` | `MINIMAX_REAL_E2E_MODEL` |
| ElevenLabs | `RUN_ELEVENLABS_REAL_E2E=1` | `ELEVENLABS_API_KEY` | n/a (audio only) |
| Deepgram | `RUN_DEEPGRAM_REAL_E2E=1` | `DEEPGRAM_API_KEY` | n/a (audio only) |

CI should run real-API tests on cadence (nightly), not on every PR, so
secret rotation doesn't gate development.
