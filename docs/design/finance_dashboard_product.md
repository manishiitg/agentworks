# Personal Finance Dashboard: a consolidated view across finance workflows

**Status:** Concept (2026-08-16). Not started.

## The idea

A number of existing workflows each track one slice of personal finance.
Each already has its own `db/db.sqlite` with real, populated data. The idea
is a single dashboard that consolidates them into one view, plus a chat
panel (later, not v1) to ask questions across all of them.

**Scope: bank accounts, investments (mutual funds), and tax.** Trading
(`globalinvestment`, `trading`) is explicitly out — a different domain
(positions, P&L, strategies), not part of this dashboard.

**Single-user, own data, dashboard-only v1.** Not a client-facing product —
no permission model, no untrusted content, no write barrier to design, and
(per the 2026-08-16 decisions) no chat in the first slice either. That makes
it the simplest of the product concepts explored this session, and a good
candidate for the first one actually built.

## Chat (built 2026-08-17) — needed a real backend after all

The dashboard itself stayed frontend-only, as designed below. Chat did not
-- an LLM turn needs the platform's agent infrastructure, so this added a
new `agent_go/internal/financeproduct/` package: a `product.yaml`-declared
profile (id `finance`), one custom tool (`finance.query-source`, backend
name `query_finance_source`) that maps a whitelisted `source` argument
(hdfc/icici/mutual_fund/tax/gst) to the same five hardcoded db paths the
frontend adapters use, and runs a read-only SQL query via
`workspace.Client.QueryAuthorizedWorkflowDB` -- the same read-only-enforced
primitive as everywhere else in this doc. A real system prompt
(`prompts/system-prompt.md`) documents every source's actual schema and
known data-quality caveats (the dirty ICICI currency string, HDFC's
JSON-blob transaction field, mutual fund's untracked cost basis, GST's
re-observed-per-snapshot duplication, tax notices having no resolved
status) so the agent doesn't have to rediscover them the hard way.

**Chat is deliberately not part of the frontend-only architecture below.**
Everything under "Architecture" and "The card types" still describes the
dashboard; this section is additive.

### One already-known class of problem, one genuinely new one

Before writing any of this up as a finding, it should have been checked
against `docs/design/product_api_transport_for_coding_agents.md` and
`docs/bugs/hybrid_profile_told_it_has_no_shell.md` -- both already exist,
both cover most of what showed up here. Corrected version:

1. **Superseded by a third correction (2026-08-17): `transport` was never
   the actual security boundary.** The original claim here (native/tmux
   transport lets a coding CLI's own tool loop bypass `tool_policy`
   entirely) was re-tested live under the *actual* fix
   (`agent_tools.mode: mcp_only`, point below) with `transport: auto`
   (native/tmux) restored: a fresh codex-cli session still made 12 `exec`
   tool calls, but every one resolved through `get_api_spec`/the
   code-execution-mode HTTP bridge -- the same registered-tool surface
   `tool_policy` governs -- not a raw host shell. A `node_repl` attempt was
   flatly rejected as unregistered. No unauthorized tool call ever
   executed. Root cause of `mcpagent`'s own containment: `mcp_only` calls
   `WithCodexDisableShellTool()` (`mcpagent/agent/coding_agent_
   integrations.go:146-148`), which disables codex's native `shell_tool`
   via codex's own CLI `--disable` flags, unconditionally, regardless of
   transport. So the original 12-tool-call leak this profile saw almost
   certainly happened while Finance was still `scope: global` (point 3
   below) -- global scope was independently confirmed to break
   `provider_options`/prompt binding, meaning the *registered tool set
   itself* was plausibly broader than intended at the time this leak was
   observed, not that transport let codex bypass a correctly-scoped
   `tool_policy`. Video Studio's `transport: auto` + `agent_tools.mode:
   mcp_only` is therefore **not** the live gap an earlier version of this
   doc flagged it as -- `mcp_only` appears to correctly contain codex-cli
   regardless of transport. `transport: structured` is kept for Finance
   anyway (turn-based, easier to debug than a long-lived tmux session),
   just not for the reason originally written down.
2. **The second tool the model reached for, `nodeRepl`/`js`, is section
   4 of `hybrid_profile_told_it_has_no_shell.md`, "personal MCP servers
   leak into product sessions" -- not codex's own intrinsic toolset**,
   which was the wrong attribution in an earlier version of this doc. The
   captured tool call's own code read `nodeRepl.write({cwd: nodeRepl.cwd,
   ...})` -- the exact `tools.mcp__node_repl__js` tool that bug doc
   already names, sourced from a developer's personal `~/.codex/config.toml`,
   not from codex-cli itself. That doc's own measurement of codex's actual
   sealed sandbox (`functions.exec`: `typeof fetch → undefined`, no
   network, no env) is a *different* tool from `node_repl`, and is why this
   session's `js` call had a working `fetch` where the bug doc's did not --
   confirms, not contradicts, that these are two different tools. Practical
   consequence: **this specific escape is very likely a local-dev-machine
   artifact** (whichever machine ran this test has a personal `node_repl`
   MCP server configured), not something a real deployed Finance user would
   hit unless they too had one configured. The already-tracked fix
   (`--ignore-user-config` or an isolated `CODEX_HOME`) still applies and
   is unresolved platform-wide, not specific to Finance.
3. **This one does appear genuinely new**: global scope makes
   `provider_options` decorative, not authoritative -- by design, not a
   bug. `resolveAgentProfileForQuery`'s own logic: `if isGlobalScope &&
   requestHasExplicitModel { provider, modelID = req.Provider, req.ModelID
   }` -- the user's own chat-level selection always wins for a
   global-scoped profile, unconditionally. That's exactly right for Chief
   of Staff (the whole point of its own design) and exactly wrong for
   Finance, whose safety story depends on the provider actually being
   restricted. Global scope also takes the dynamic multi-agent delegation
   prompt instead of this profile's own `prompt.file` -- confirmed live
   that an earlier global-scoped version of this profile never sent
   `prompts/system-prompt.md` to the model at all. **Finance is
   `scope: project`, not global**, even though it spans multiple sources
   the same shape-of-reason Chief of Staff does -- project scope's usual
   narrowing (folder guard collapsed to one root) is a non-issue here
   since this profile's one tool bypasses `FolderGuard` entirely via its
   own hardcoded path whitelist. Neither of the two transport docs above
   covers scope/`provider_options` interaction at all -- this is the one
   piece of this investigation that earns being called a finding rather
   than a rediscovery.

**The fix, in the product.yaml `runtime:` block**: `agent_tools.mode:
mcp_only` is the actual security boundary (see the correction above);
`transport: structured` is a Finance-specific debuggability choice kept
alongside it, not itself load-bearing. `agent_tools.mode: mcp_only`
(explicit -- this one genuinely does match Video Studio's
current actual value, confirmed directly, though Video Studio's own
`tool_policy` comment describes its setting as "Hybrid," contradicting its
own `agent_tools.mode: mcp_only` a few lines below -- an internal
documentation drift in Video Studio's own file, not resolved here), and
`provider_options` curated to **exactly one** entry, `claude-code` -- not
the four-provider list Video Studio offers. That narrower curation is a
genuine, deliberate departure from Video Studio's own precedent, not just
copying it: `node_repl` being a personal-machine artifact lowers the
severity of finding #2, but doesn't retroactively prove codex-cli or
cursor-cli are safe under a real allowlist for a product where the stakes
are real financial data rather than a video project -- that hasn't been
tested, so it isn't assumed. **claude-code's own safety here is Finance's
own direct evidence, not inferred from Video Studio's precedent** -- Video
Studio's own claude-code sessions run under a *different* transport
(native/tmux, per its `transport: auto`), so its production usage does not
actually validate the exact combination Finance uses
(`transport: structured` + `agent_tools: mcp_only`). What actually backs
this is the live log evidence below: two clean turns, correctly filtered,
no leak -- not exhaustive, but a direct test of this exact configuration
rather than an inference from a different one.

### Confirmed vs. not yet confirmed (2026-08-17)

**Solidly confirmed, from direct server-log evidence:**
- `provider=claude-code hybrid=false` for every turn after the fix,
  regardless of what the frontend's (stale, cosmetic-only) model-selector
  label displayed.
- `[PRODUCT_TOOL_GATE] profile=finance ... mode=allowlist registered=1:
  query_finance_source` paired with `filtered=55: [execute_shell_command
  delegate agent_browser list_secrets create_workflow_schedule ...]` -- the
  allowlist is genuinely, actively filtering 55 other platform tools, every
  turn.
- After the fix, on claude-code, **no unauthorized tool call ever actually
  executed** -- the model correctly refused to fabricate numbers or invent
  a workaround when it couldn't reach its one tool, twice.
- Before the fix, on codex-cli, the model's own native `exec`/`js` calls
  *did* run genuinely, 12 times, with real output (one result was 37,846
  bytes). Whether that was actually contained to something harmless is
  **not established** -- the tmux launch command showed `--sandbox
  workspace-write` and the `js` call's own reported `cwd` was scoped to
  the Chats folder, but this investigation did not verify path-traversal
  resistance or test for an actual escape, only observed that the turns it
  happened to run were benign (balance-checking, not adversarial probing).
  Treat the pre-fix codex-cli leak as a real, not fully characterized risk,
  not as "confirmed safe because nothing bad happened this time."

**Not yet cleanly confirmed: a truly fresh session successfully calling
`query_finance_source` and returning real data.** Every post-fix test
attempt inherited a resumed claude-code thread (the platform's session
restore matches by title/workspace, not just a cleared local tab) carrying
forward an incorrect "I don't have this tool" belief from earlier,
differently-configured testing -- the model never even attempted the tool
call in that state, just repeated its earlier claim. This is very likely a
session-continuity artifact of live-testing through several config changes
in one dev session, not a reflection of a genuine first-time user's
experience, but it was not cleanly proven before this investigation had to
stop. **This is a real open item**, and also a real platform finding worth
someone else's attention: if a resumed session can carry forward a stale
belief about its own tool availability across a server restart and profile
change, that could affect any product relying on session resume after a
mid-conversation reconfiguration, not just Finance.

**Next step before relying on this**: open Finance chat from a genuinely
clean state (no prior session under this title/workspace, e.g. a different
`_users/<id>/` or a purged conversation file) and confirm
`query_finance_source` is actually called and returns correct data end to
end. Do not assume it works from the tool-gate log alone.

## Architecture: no new backend, no Go files (dashboard only)

**The whole thing is frontend-only.** `agentApi.queryWorkflowDB(dbPath,
sql)` (`frontend/src/services/api.ts:1487`) already exists, already works
from any React component, and already takes an explicit `dbPath` — it is a
plain `POST /api/query` call, nothing iframe- or report-specific. Proof: the
workflow report system's own `window.report.query` is *implemented as* a
thin wrapper around this exact function
(`frontend/src/components/workflow/ReportViewer.tsx:88-89`:
`query: async (sql) => agentApi.queryWorkflowDB(\`${workspacePath}/db/db.sqlite\`, sql)`).
`window.report` exists only to hand that function into an
**iframe-sandboxed** HTML document — a constraint this product doesn't have,
since it's a real React surface, not agent-authored HTML (see
`workflow_custom_ui_product.md` for where that distinction matters). So:
call `agentApi.queryWorkflowDB` directly, no bridge, no iframe, no new
backend route, no Go package, no product.yaml (nothing here needs its own
agent identity while there's no chat).

```
frontend/src/products/finance/
  adapters/
    hdfc.ts        → agentApi.queryWorkflowDB('Workflow/HDFC-Personal-Accounts/db/db.sqlite', sql)
    icici.ts        → same, ICICI's path + SQL
    mutualFund.ts   → same, Mututal-Fund's path + SQL
    tax.ts          → same, check-form-26as-xspaces's path + SQL
    gst.ts          → same, gstdatacollection's path + SQL
  parseIciciAmount.ts → the one piece of real logic; own unit test (see below)
  types.ts          → the per-source card types + the aggregate types
  synthesize.ts      → pure function: five adapters' output -> NetWorthSummary,
                        AttentionItem[], ActivityEvent[] -- the actual point,
                        see "Synthesis, not just parallel cards" below
  FinanceSurface.tsx → calls all adapters in parallel, synthesizes, renders
                        synthesis views first, per-source sections below
```

Every adapter is a small `.ts` file: a `dbPath` constant, one or two
`SELECT`s, and a mapper into a typed card. Buildable and testable entirely
against real data with no server restart — `agentApi.queryWorkflowDB` talks
to the already-running workspace server.

## The card types — designed against all five real schemas at once

There is no single unifying struct. Bank/investment data and tax/GST data
don't share a "balance" concept, and even within banking, HDFC only stores
a transaction *count* while ICICI stores real transaction rows — a
transaction list can't be a shared concept either, only some sources
support it. Real schemas, confirmed 2026-08-16
(`workspace-docs/Workflow/*/db/db.sqlite`):

| Workflow | Domain | Real columns (non-platform tables) |
|---|---|---|
| `HDFC-Personal-Accounts` | bank | `balance_history(group_name, current_balance, total_fixed_deposit, updated_at_iso)`; `transaction_summary(group_name, total_transactions, latest_month, latest_transaction)` — **count only, no transaction rows** |
| `ICICI-BANK-PARSING` | bank | `current_balances(group_name, account_name, account_number, total_balance_inr, fd_balance)`; `recent_transactions(group_name, txn_date, description, amount_inr, cr_dr, closing_balance)` — **real per-transaction rows** |
| `Mututal-Fund` | investments | `portfolio_holdings(group_name, scheme_name, folio_number, units, current_value, invested_value, nav, profit_loss)`; `account_xirr(group_name, xirr_pct, as_of_date)` |
| `check-form-26as-xspaces` | tax | `tax_summary(pan, total_tds_current_ay, pending_notice_count, total_refund_amount, last_checked)`; `notices(pan, din, title, status, issue_date, action_required)` |
| `gstdatacollection` | tax | `gst_snapshot(gstin, legal_name, turnover_aggregate)`; `gst_ledger_balance(igst, cgst, sgst, cess)`; `gst_return_status(fin_year, period, due_date, status, filed_date)` |

```ts
type BankAccountCard = {
  source: 'HDFC' | 'ICICI'
  accountLabel: string        // group_name / account_name
  currentBalance: number
  fixedDeposit: number
  lastUpdated: string
  // HDFC has no transaction rows, only a count -- carried here, not faked
  // into a transaction list:
  transactionCount?: number
  lastTransactionDate?: string
}

type RecentTransaction = {   // ICICI only -- HDFC's adapter emits none
  source: string
  date: string
  description: string
  amountInr: number
  direction: 'credit' | 'debit'
}

type InvestmentHolding = {   // mutual funds
  schemeName: string
  folioNumber: string
  units: number
  currentValue: number
  investedValue: number
  profitLoss: number
}

type InvestmentAccountCard = {
  source: 'Mutual Fund'
  accountLabel: string        // group_name
  totalCurrentValue: number
  totalInvested: number
  xirrPct?: number
  holdings: InvestmentHolding[]
}

type TaxCard = {
  pan: string
  tdsThisYear: number
  pendingNotices: number
  refundAmount: number
  lastChecked: string
}

type GstCard = {
  gstin: string
  legalName: string
  turnoverAggregate: number
  ledgerBalance: { igst: number; cgst: number; sgst: number; cess: number }
  nextReturnDue?: string
  filingStatus?: string
}

// Computed by combining the cards above -- not queried from any one
// source. See "Synthesis, not just parallel cards" below.
type NetWorthSummary = {
  liquid: number       // Σ bank current balances
  locked: number        // Σ fixed deposits
  invested: number      // Σ mutual-fund current value
  total: number
}

type AttentionItem = {
  source: 'GST' | 'Tax'
  severity: 'overdue' | 'pending'
  label: string          // e.g. "GSTR-3B, June 2026 -- not filed"
  dueDate?: string
}

type ActivityEvent = {
  source: 'ICICI' | 'Mutual Fund'
  date: string
  description: string
  amount: number
}
```

Not every workflow in the workspace is a finance source despite the
name/location — `ICICI-BANK-PARSING-v2` and `customer-login` turned out to be
unrelated QA/verification workflows on inspection. **The source list is
curated by hand, never auto-discovered** — no naming convention is reliable
enough to infer it, and silently pulling in the wrong data is a real risk.

## Synthesis, not just parallel cards

Five card sections on one page is "on one page," not "consolidated." The
actual point of combining sources is the views that only exist *because*
they're combined. Grounded in real query results, 2026-08-16:

**1. Net Worth — one number.** HDFC and the mutual-fund source already
self-aggregate across their own multiple accounts
(`portfolio_summary.total_portfolio`, `portfolio_overview.total_portfolio_value`
— both are already rollup rows, not per-account data). ICICI has no such
rollup; sum `current_balances.total_balance_inr` per account after parsing
it (see the currency gotcha below). Net worth = HDFC's total + Σ ICICI
accounts + the mutual-fund total.

**2. Liquid vs. Locked vs. Invested.** A real allocation view that exists
only by combining sources: bank current balances (liquid) vs. fixed
deposits (locked) vs. mutual-fund current value (invested). No single
source has this shape.

**3. Needs Attention — one prioritized list, not buried per-card.** Pulled
from a real query against `gst_return_status` and `notices`, 2026-08-16: a
`GSTR-3B` return for June 2026 is still `Not Filed`, past its due date, and
at least one tax notice has `status: "Response Due"` with
`action_required: 1`. These belong at the top of the dashboard as one
merged list, not inside separate GST and tax cards where they're easy to
miss.

**4. A cross-source staleness check.** Each source has its own freshness
timestamp (`updated_at_iso` / `generated_at` / `sync_date`, named
differently everywhere as usual). If one source is much older than the
others, say so — a number silently shown next to fresher ones is a subtle
way to make a bad decision.

**5. A unified activity feed.** Merge ICICI's real `recent_transactions`
rows with the mutual fund's `portfolio_transactions` into one chronological
list. HDFC has no transaction rows — only `transaction_summary`'s count and
latest date — so it contributes one summary line rather than being silently
dropped from the feed.

**Deliberately not attempted: a tax-liability-vs-available-cash
calculation.** `tax_summary.total_tds_current_ay` and the refund fields
don't cleanly net out to "amount you still owe" without real tax-domain
logic this doc has no grounding in — guessing at that for a page that
touches actual money decisions is worse than not showing it.

**A real parsing gotcha, not hypothetical:** ICICI's `total_balance_inr` is
formatted text, e.g. `"INR 12,34,567.89CR"` — a currency prefix, Indian
comma grouping, and a credit/debit suffix glued directly onto the number
with no space. Comma-stripping is safe for the grouping (JS `parseFloat`
doesn't care where commas fall), but the `CR`/`DR` suffix must be stripped
before parsing or it silently produces `NaN`. This needs a real parser with
a test, not an inline `parseFloat`.

## Decisions (2026-08-16)

| Question | Decision |
|---|---|
| Scope | Bank accounts, investments (mutual funds), and tax — trading is a different domain, out |
| Which sources feed it | A curated list named by hand, one adapter per source — not auto-discovery |
| Where it lives | A new product surface, own tile — a real dashboard needs layout a chat+aside shape doesn't fit |
| Backend (dashboard) | None — frontend-only, `agentApi.queryWorkflowDB` directly, no Go, no new routes |
| Backend (chat) | A real `agent_go/internal/financeproduct/` package — built 2026-08-17, see "Chat" above. Chat needs an LLM turn, the dashboard doesn't. |
| Chat | Built 2026-08-17: `scope: project` (not global — see "Chat" above for why), one custom read-only tool, `provider_options` curated to `claude-code` only. Functional end-to-end verification still open — see "Confirmed vs. not yet confirmed". |
| Branch | Continues on `feature/chief-of-staff-product` (not split to its own branch) |

## Build order

The reuse rule from `reusable_vertical_product_platform.md` ("extract after
a second consumer demonstrates the common behavior") does not counsel
staging schema design here — it exists to avoid *guessing* about an unknown
future consumer. All five sources are already fixed and already inspected
(table above), so there is nothing left to discover by building one adapter
first that isn't already visible in the schema dump. Building only HDFC's
adapter first would mean designing the card types around one source and
finding out later they don't fit tax or investment data — the exact mistake
sequencing exists to prevent, self-inflicted instead of avoided.

1. **The card types and the aggregate types** (above) — already designed
   against all five schemas together, not one at a time.
2. **The currency parser, with a test, before anything sums it.** ICICI's
   `total_balance_inr` is dirty text (see the gotcha above); Net Worth is
   wrong the moment this is wrong, silently. This is the one piece of real
   logic in the whole build and it deserves its own unit test with the
   actual observed format as a fixture, not a synthetic clean string.
3. **Write all five adapters together** — each a small `.ts` file, a
   `dbPath` constant, a couple of `SELECT`s, a mapper.
4. **One dashboard card, wired end to end, first** — pick one source (a
   bank account is the simplest) and render it before wiring the rest, so
   the `agentApi.queryWorkflowDB` path and the product surface itself are
   proven before building on top of them. This is the only place staging
   still earns its keep: it isolates *integration* risk, not *schema* risk,
   which step 1 already resolved.
5. **The synthesis layer** (Net Worth, allocation split, Needs Attention,
   staleness check, unified activity feed) — computed from the five
   adapters' combined output, not queried fresh. This is the actual point
   of the dashboard; do not treat it as a stretch goal after step 6.
6. **The full page** — synthesis views first/top, per-source card sections
   below them for anyone who wants to drill into one source.
7. **(Later, separate slice) Chat.** When it's wanted: read-only tool
   surface reusing these same adapters' queries, narrower than workflow
   `run` mode since nothing here should ever execute a workflow. See
   `workflow_custom_ui_product.md`'s Gap 1 finding if execution is ever
   added later — the write-barrier concern there doesn't apply to
   read-only queries, but would apply the moment "run/refresh" is added.

## Open questions

- Refresh cadence: does the dashboard query live on every load, or does it
  need caching once five sources' worth of queries per page load is
  noticeable? Five is small enough this is probably premature — revisit if
  it's actually slow.
- Where do the card types/adapters live if a second, unrelated consolidation
  surface is ever wanted — still `frontend/src/products/finance/`, or
  extracted? Per the reuse rule: wait for a second real consumer.

## Related

- `docs/design/workflow_custom_ui_product.md` — a harder, different concept
  (client-facing custom UI *per workflow*, with a real write barrier and
  permission model to design, and a genuine agent-authored-HTML sandboxing
  question). This dashboard avoids all of that: one first-party UI spanning
  several trusted sources, read-only, no backend at all in v1.
- `docs/design/reusable_vertical_product_platform.md` — the reuse rule
  invoked (and explained why it doesn't apply to schema design here) above.
- `docs/design/product_api_transport_for_coding_agents.md` — the existing,
  much deeper investigation of native/tmux vs. structured transport across
  all four providers; check this before writing up any future
  transport/tool-exposure finding as new.
- `docs/bugs/hybrid_profile_told_it_has_no_shell.md` — section 4 is the
  `node_repl`/personal-MCP-server leak this profile also hit; the fix
  (`--ignore-user-config` or an isolated `CODEX_HOME`) is still unresolved
  platform-wide, not specific to Finance.
