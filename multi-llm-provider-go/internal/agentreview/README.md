# Agentic test validation (`agentreview`)

## Why this exists

Coding-CLI behavior changes with **every release** (Claude Code, Codex, Cursor, Pi). A test that only makes **deterministic assertions** can pass on visibly-degraded output:

- The Codex transcript streamer once emitted **every assistant line twice** (Codex writes each message as both an `event_msg/agent_message` *and* a `response_item/message`). The deterministic checks — "file written", "≥2 content chunks", "≥2 distinct tools", "final contains code word" — **all passed**. The stream was garbage, but green.

Deterministic asserts verify *facts* (a file exists, a tool ran). They cannot verify *quality* (is the narration coherent? are lines duplicated? is the interleaving natural?). For live coding-agent output, **an agent must actually look at the output and sign off.**

## The rule for future tests

> When you add or change a **live coding-agent test** whose value is the *shape/quality of model output* (streaming, interleaving, formatting, tool-use narration), do **not** rely on deterministic assertions alone. Record the real output and require an **agent review**. The agent running the tests reads the recorded output, judges it, and signs off in JSON.

## How it works

1. The live test builds the real captured output and calls:
   ```go
   rec := agentreview.Write(t, "<TestName>", "<summary>",
       map[string]any{ /* the REAL output: streamed_content, order, tools, final, file-on-disk ... */ },
       map[string]any{ /* the stable SHAPE to fingerprint: distinct tools, etc. */ })
   agentreview.RequireReviewed(t, rec)
   ```
2. `Write` saves `testdata/agent-reviews/<TestName>.json` — the real output plus an `agent_review` block — and stamps a **fingerprint** over the *stable shape* (not random tokens, so it doesn't churn every run).
3. `RequireReviewed` **fails** unless an agent has set `agent_review.verdict = "good"` with `reviewed_fingerprint` equal to the current fingerprint.
4. When a CLI release changes behavior, the **fingerprint changes**, the stored sign-off goes **stale**, and the gate fails again — forcing a fresh review. (This is the whole point: *agent releases change behavior*, so approvals must not be permanent.)

## Review criteria

Every record carries `review_criteria` (`agentreview.StreamingCriteria`). The agent must check the recorded `output` — especially the discrete `content_chunks` — against all of them:

- no duplicated lines or chunks;
- **proper formatting** — clean segmentation, no run-on/garbled/merged text, no stray control chars or terminal escapes;
- **human-readable** — reads like an assistant working, coherent and natural;
- correct text ↔ tool interleaving order (narration then the tool it describes);
- the tool calls are the real intended tools (not leaked internal/shell noise where MCP tools were expected);
- the final answer is coherent and matches the work performed;
- real work actually happened (e.g. the file was written to disk).

## Workflow — one script, self-enforcing

```
# 1. Capture: run the live tests, record real output, RESET every review to pending.
scripts/agentic-p0.sh capture

# 2. An agent reads each pkg/adapters/*/testdata/agent-reviews/*.json — the discrete
#    content_chunks against review_criteria above — and sets:
#      agent_review.verdict = "good"  (or "bad" with issues)
#      agent_review.reviewed_fingerprint = <the record's fingerprint>
#      agent_review.reviewer = "<who>"

# 3. Verify: cheap gate (no live CLI). Fails until EVERY record is approved.
scripts/agentic-p0.sh verify
```

`capture` **resets** reviews to pending every run, so a fresh suite run always starts "unreviewed" — any agent, now or after a future CLI release, is forced to look at the output. `TestAgentReviewsApproved` (per adapter) is the gate; its failure message tells the agent exactly what to do. The review files are committed; on a CLI upgrade, re-capture and re-review (a changed fingerprint also invalidates a stale sign-off).

## What this caught (worked example)

The Codex real-world streaming review record showed `"...ZEBRA_xxxxZEBRA_xxxx"` and a `stream_order` ending `[... text text]` — the doubled output. Deterministic asserts missed it; **reading the recorded output caught it**, twice (first the doubled narration, then a residual doubled final line), leading to the text-keyed dedup fix. Both records are now signed off as `good`.
