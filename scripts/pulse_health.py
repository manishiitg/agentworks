#!/usr/bin/env python3
"""Pulse health / loop diagnostic across every workflow's Pulse state.

Answers one question: **is Pulse converging, or looping?**

The loop this exists to measure: a fix reaches `changed_unverified` ->
`awaiting_verification`, which only a producing run can settle. If runs keep
dying (PLAT-054), evidence never arrives, the next pass re-examines the same
finding, re-fixes it, and still cannot prove it. Upwork's baseline showed 91
`fix_started` against 69 `closed` with 64 `verification_inconclusive` -- that
ratio is the loop rendered as counts.

Sections:
  1. LOOP HEALTH    -- per workflow: closure rate, verification debt, recurrence
  2. CATEGORY       -- findings grouped by classification/issue_kind + step spread
  3. COHERENCE      -- contradictory states Go cannot retroactively repair
  4. UNTRIAGED      -- external_action_required findings citing no PLAT-NNN

Usage:
    python3 scripts/pulse_health.py                       # full report
    python3 scripts/pulse_health.py --json baseline.json  # + write a snapshot
    python3 scripts/pulse_health.py --baseline baseline.json   # + cohort diff

**Read the cohort diff, not the totals.** Reviewer lenses can be disabled at the
Gate (currently: only `workflow_review` runs), which lowers the finding count on
its own and has nothing to do with whether a platform fix worked. `--baseline`
tracks the specific fingerprints that were open at baseline and reports how many
of *those* closed, which survives a lens being switched off. Filing rate is
reported separately, per module, so a drop caused by a disabled lens is
visible as such.

Always read-only: every database is opened with mode=ro.
"""
import argparse
import collections
import datetime
import json
import re
import sqlite3
import sys
from pathlib import Path

PLAT_ID_RE = re.compile(r"PLAT-\d+", re.IGNORECASE)

# Terminal for the workflow's own lifecycle.
CLOSED_STATUSES = {"resolved", "rejected"}
# Unblocked directly by a producing run -- the primary PLAT-054 signal.
RUN_GATED_STATUSES = {"awaiting_run", "awaiting_verification"}
# Need Engineering Review passes; capacity-capped, drains slowly.
ENG_QUEUE_STATUSES = {"queued_for_engineering", "open", "acknowledged", "fixing"}
# Need platform code (a PLAT ticket). Producing runs never close these.
PLATFORM_STATUSES = {"external_action_required"}

# Retired module names still present in historical rows. The canonical four are
# workflow_review, llm_ops_review, strategy_auditor, goal_advisor; everything
# else here was consolidated into them.
RETIRED_MODULES = {
    "bug_review", "stores_health", "artifact_review", "eval_health",
    "report_health", "learning_health", "cost_llm_time",
}

# Modules currently suppressed at the Gate (see the temporary block in
# agent_go/cmd/server/guidance/templates/system/pulse-gate.md). A finding owned
# by a disabled lens is FROZEN: nothing will re-review it, so it cannot close no
# matter how well a platform fix works. Counting those as failures-to-close is
# the second way a working fix gets blamed. Keep in sync with the Gate, or
# override with --disabled-modules.
DEFAULT_DISABLED_MODULES = {"llm_ops_review", "goal_advisor", "strategy_auditor"}
DISABLED_MODULES = set(DEFAULT_DISABLED_MODULES)

EVENT_KEYS = [
    "filed", "fix_started", "closed", "verification_inconclusive",
    "observed_again", "reopened", "queued_for_engineering",
    "merged_duplicate", "duplicates_merged", "external_action_required",
]


def connect_ro(path: Path):
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


def scan_step_coverage(workflow_dir: Path, recent_runs: int = 5) -> dict:
    """Which plan steps actually executed recently.

    Routes nest and compose, so enumerating end-to-end paths is hopeless AND
    unnecessary: linkedin has 6 routers / 16 branches / 256 naive path
    combinations, and its runs executed 2, 6, and 1 of 24 steps. What every
    finding is actually keyed to is a step_id, so the only question that matters
    is "has that step run since this finding was filed?" -- a flat observable
    that does not care how many routers exist or how deeply they nest.

    Without this, an unexercised step's finding looks identical to a failed fix.
    """
    coverage = {"plan_steps": [], "executed": {}, "runs_scanned": []}

    plan_path = workflow_dir / "planning" / "plan.json"
    if plan_path.exists():
        try:
            plan = json.loads(plan_path.read_text(errors="replace"))
            for step in plan.get("steps", []):
                sid = step.get("id") or step.get("step_id")
                if sid:
                    coverage["plan_steps"].append(sid)
        except (json.JSONDecodeError, OSError):
            pass

    runs_dir = workflow_dir / "runs"
    if not runs_dir.is_dir():
        return coverage

    # Most recently modified run folders, newest first.
    run_dirs = sorted(
        (d for d in runs_dir.iterdir() if d.is_dir()),
        key=lambda d: d.stat().st_mtime,
        reverse=True,
    )[:recent_runs]
    for run_dir in run_dirs:
        coverage["runs_scanned"].append(run_dir.name)
        # runs/<iteration>/<group>/execution/<step-id>/ — the group layer varies,
        # and `archived/` holds superseded attempts of the same run.
        for exec_dir in run_dir.glob("*/execution"):
            for step_dir in exec_dir.iterdir():
                if not step_dir.is_dir() or step_dir.name in ("archived", "Downloads"):
                    continue
                coverage["executed"].setdefault(step_dir.name, []).append(run_dir.name)
    return coverage


def finding_is_plan_step(finding: dict, coverage: dict) -> bool:
    """A finding keyed to a real plan step is gated on that step running. One
    keyed to a reviewer module (workflow_review, eval_health, ...) is not -- it
    is settled by Pulse re-reviewing artifacts, so step coverage says nothing
    about it."""
    return finding.get("module", "") in set(coverage.get("plan_steps", []))


def table_exists(conn, name: str) -> bool:
    row = conn.execute(
        "select 1 from sqlite_master where type='table' and name=?", (name,)
    ).fetchone()
    return row is not None


def load_register_ids(repo_root: Path) -> set:
    """Every PLAT-NNN already known, so a finding citing one isn't re-flagged."""
    ids = set()
    # Every ticket is a file under docs/bugs/pulse_platform/<product>/<area>/ (no register since 2026-10-06).
    for ticket in (repo_root / "docs" / "bugs" / "pulse_platform").rglob("plat-*.md"):
        ids |= {m.upper() for m in PLAT_ID_RE.findall(ticket.name)}
    frag_dir = repo_root / "docs" / "bugs" / "pulse_platform"
    if frag_dir.exists():
        for f in frag_dir.rglob("plat-*.md"):
            ids |= {m.upper() for m in PLAT_ID_RE.findall(f.read_text(errors="replace"))}
    return ids


def scan_workflow(db_path: Path) -> dict:
    """One workflow's complete Pulse state. Missing tables degrade to empty."""
    data = {
        "events": collections.Counter(),
        "findings": {},          # fingerprint -> {status, module, issue_kind, ...}
        "recurrence": {"seen_gt1": 0, "seen_ge3": 0},
        "module_state": {},      # module -> {gate_decision, decision, result, pulse_run_id, checked_at}
    }
    try:
        conn = connect_ro(db_path)
    except sqlite3.OperationalError:
        return data

    try:
        if table_exists(conn, "pulse_module_state"):
            for module, pulse_run_id, checked_at, gate_decision, decision, result in conn.execute(
                """select module, last_pulse_run_id, last_checked_at,
                          coalesce(last_gate_decision,''), coalesce(last_decision,''), coalesce(last_result,'')
                   from pulse_module_state"""
            ):
                data["module_state"][module] = {
                    "pulse_run_id": pulse_run_id or "",
                    "checked_at": checked_at or "",
                    "gate_decision": gate_decision,
                    "decision": decision,
                    "result": result,
                }
    except sqlite3.OperationalError:
        pass

    try:
        if not table_exists(conn, "run_concerns"):
            return data

        has_details = table_exists(conn, "pulse_finding_details")
        if has_details:
            query = """
                select c.fingerprint, c.status, c.step_id, c.phase, c.seen_count, c.text,
                       coalesce(d.issue_kind, ''), coalesce(d.detail_json, '')
                from run_concerns c
                left join pulse_finding_details d on d.fingerprint = c.fingerprint
            """
        else:
            query = """
                select c.fingerprint, c.status, c.step_id, c.phase, c.seen_count, c.text,
                       '', ''
                from run_concerns c
            """
        for fp, status, step_id, phase, seen, text, issue_kind, detail_json in conn.execute(query):
            classification = severity = target_key = summary = ""
            evidence = ""
            if detail_json:
                try:
                    d = json.loads(detail_json)
                except json.JSONDecodeError:
                    d = {}
                classification = d.get("classification", "") or ""
                severity = d.get("severity", "") or ""
                target_key = d.get("target_key", "") or ""
                summary = d.get("summary", "") or ""
                evidence = " ".join(d.get("evidence") or [])
                # detail_json is authoritative when the column and blob disagree
                # (MergePulseFindingIssues refreshes the blob but not the column).
                issue_kind = (d.get("issue_kind", "") or issue_kind or "").strip()
            seen = seen or 0
            if seen > 1:
                data["recurrence"]["seen_gt1"] += 1
            if seen >= 3:
                data["recurrence"]["seen_ge3"] += 1
            data["findings"][fp] = {
                "status": status or "",
                "module": step_id or "",
                "phase": phase or "",
                "seen_count": seen,
                "issue_kind": issue_kind,
                "classification": classification,
                "severity": severity,
                "target_key": target_key,
                "label": target_key or summary or (text or "")[:100],
                "plat_ids": sorted({
                    m.upper() for m in PLAT_ID_RE.findall(
                        " ".join([summary, evidence, target_key, text or ""])
                    )
                }),
            }

        if table_exists(conn, "pulse_finding_events"):
            for event_type, count in conn.execute(
                "select event_type, count(*) from pulse_finding_events group by event_type"
            ):
                # Identity-merge events are suffixed (`closed:identity_merge:ab12`);
                # fold them into their base type so counts aren't fragmented.
                base = (event_type or "").split(":")[0]
                data["events"][base] += count
    finally:
        conn.close()
    return data


def bucket_counts(findings: dict) -> dict:
    active = {fp: f for fp, f in findings.items() if f["status"] not in CLOSED_STATUSES}
    return {
        "active": len(active),
        "run_gated": sum(1 for f in active.values() if f["status"] in RUN_GATED_STATUSES),
        "eng_queue": sum(1 for f in active.values() if f["status"] in ENG_QUEUE_STATUSES),
        "platform": sum(1 for f in active.values() if f["status"] in PLATFORM_STATUSES),
    }


def pct(numerator, denominator):
    if not denominator:
        return "  n/a"
    return f"{100.0 * numerator / denominator:5.1f}%"


def report_loop_health(snapshot: dict):
    print("=" * 100)
    print("1. LOOP HEALTH — is repair work finishing, or being redone?")
    print("=" * 100)
    print(f"{'workflow':<24}{'active':>7}{'run-gtd':>8}{'eng-q':>7}{'plat':>6}"
          f"{'closure':>9}{'ver.debt':>10}{'seen>1':>8}{'seen>=3':>8}")
    print("-" * 100)
    totals = collections.Counter()
    for name, wf in sorted(snapshot["workflows"].items()):
        ev = wf["events"]
        b = bucket_counts(wf["findings"])
        if not wf["findings"]:
            continue
        # Finding-based, not event-based: a finding can be closed, reopen, and
        # close again, so closed/filed events exceed 100% and measures churn
        # rather than progress. What is actually being asked is "what share of
        # everything ever raised is now settled".
        closed_findings = sum(1 for f in wf["findings"].values()
                              if f["status"] in CLOSED_STATUSES)
        closure = pct(closed_findings, len(wf["findings"]))
        debt = pct(ev.get("verification_inconclusive", 0), ev.get("fix_started", 0))
        r = wf["recurrence"]
        totals["closed_findings"] += closed_findings
        totals["all_findings"] += len(wf["findings"])
        print(f"{name:<24}{b['active']:>7}{b['run_gated']:>8}{b['eng_queue']:>7}{b['platform']:>6}"
              f"{closure:>9}{debt:>10}{r['seen_gt1']:>8}{r['seen_ge3']:>8}")
        for k, v in b.items():
            totals[k] += v
        for k in EVENT_KEYS:
            totals[k] += ev.get(k, 0)
        totals["seen_gt1"] += r["seen_gt1"]
        totals["seen_ge3"] += r["seen_ge3"]
    print("-" * 100)
    print(f"{'TOTAL':<24}{totals['active']:>7}{totals['run_gated']:>8}{totals['eng_queue']:>7}"
          f"{totals['platform']:>6}{pct(totals['closed_findings'], totals['all_findings']):>9}"
          f"{pct(totals['verification_inconclusive'], totals['fix_started']):>10}"
          f"{totals['seen_gt1']:>8}{totals['seen_ge3']:>8}")
    print()
    print("  run-gtd = awaiting_run/awaiting_verification — a producing run closes these (PLAT-054's signal)")
    print("  eng-q   = queued_for_engineering/open/acknowledged — needs Engineering passes, drains slowly")
    print("  plat    = external_action_required — needs a PLAT ticket; runs will NEVER close these")
    print("  closure = share of all findings ever raised that are now resolved/rejected")
    print("  ver.debt= verification_inconclusive/fix_started — share of repair work that could not be proven")
    print()
    print(f"  Lifecycle events (all workflows): " +
          ", ".join(f"{k}={totals[k]}" for k in EVENT_KEYS if totals[k]))
    print()


def report_categories(snapshot: dict, min_count: int):
    print("=" * 100)
    print(f"2. CATEGORY ROLLUP — active findings by classification (>= {min_count}); "
          "many findings + many steps = root-cause candidate")
    print("=" * 100)
    print("Instagram's 11 findings were one root cause (PLAT-056). Step-by-step that was invisible;")
    print("a category spanning many steps is what makes a shared cause obvious without reading each row.")
    print()
    for name, wf in sorted(snapshot["workflows"].items()):
        active = [f for f in wf["findings"].values() if f["status"] not in CLOSED_STATUSES]
        if not active:
            continue
        groups = collections.defaultdict(list)
        for f in active:
            key = (f["classification"] or "(untyped)", f["issue_kind"] or "(unset)")
            groups[key].append(f)
        rows = [(k, v) for k, v in groups.items() if len(v) >= min_count]
        if not rows:
            continue
        print(f"--- {name} ---")
        for (classification, kind), items in sorted(rows, key=lambda kv: -len(kv[1])):
            steps = sorted({f["module"] for f in items if f["module"]})
            spread = f"{len(steps)} step(s)"
            shown = ", ".join(steps[:4]) + ("…" if len(steps) > 4 else "")
            print(f"  {len(items):3d}  {classification:<28} [{kind:<14}] {spread:>10}  {shown}")
        print()


def report_step_coverage(snapshot: dict):
    """Separates 'the fix failed' from 'the step never ran'."""
    print("=" * 100)
    print("2b. STEP COVERAGE — can these findings even be verified by recent runs?")
    print("=" * 100)
    print("Routes nest, so paths are not enumerable (linkedin: 6 routers, 256 combos) and not the right")
    print("unit. A finding is keyed to a step; if that step has not executed recently, the finding is")
    print("UNTESTED, not failed. Reading those as failures is how a working fix gets blamed.")
    print()
    print(f"{'workflow':<24}{'steps':>7}{'ran':>6}{'idle':>6}{'step-findings':>15}{'unexercised':>13}")
    print("-" * 100)
    for name, wf in sorted(snapshot["workflows"].items()):
        cov = wf.get("coverage")
        if not cov or not cov.get("plan_steps"):
            continue
        planned = set(cov["plan_steps"])
        ran = set(cov.get("executed", {})) & planned
        active = {fp: f for fp, f in wf["findings"].items()
                  if f["status"] not in CLOSED_STATUSES}
        step_findings = {fp: f for fp, f in active.items() if f["module"] in planned}
        unexercised = {fp: f for fp, f in step_findings.items()
                       if f["module"] not in ran}
        print(f"{name:<24}{len(planned):>7}{len(ran):>6}{len(planned) - len(ran):>6}"
              f"{len(step_findings):>15}{len(unexercised):>13}")
    print("-" * 100)
    print("  steps = plan steps · ran/idle = executed or not in the last runs scanned")
    print("  step-findings = active findings keyed to a real plan step (the rest belong to reviewer")
    print("  modules and are settled by Pulse re-review, not by any step running)")
    print("  unexercised = of those, how many are waiting on a step that has not run recently")
    print()

    # The other reason a finding cannot close, unrelated to routes or steps.
    frozen = collections.Counter()
    active_total = 0
    for name, wf in snapshot["workflows"].items():
        for f in wf["findings"].values():
            if f["status"] in CLOSED_STATUSES:
                continue
            active_total += 1
            if f["module"] in DISABLED_MODULES:
                frozen[name] += 1
    if frozen:
        print(f"  FROZEN BY A DISABLED LENS — {sum(frozen.values())} of {active_total} active findings are owned")
        print(f"  by a module currently suppressed at the Gate ({', '.join(sorted(DISABLED_MODULES))}).")
        print("  Nothing will re-review them, so they cannot close however well a platform fix works.")
        print("  Exclude them before judging closure: " +
              ", ".join(f"{k}={v}" for k, v in frozen.most_common()))
        print()
    for name, wf in sorted(snapshot["workflows"].items()):
        cov = wf.get("coverage") or {}
        if not cov.get("runs_scanned"):
            continue
        planned = set(cov.get("plan_steps", []))
        ran = sorted(set(cov.get("executed", {})) & planned)
        print(f"  {name}: last runs {', '.join(cov['runs_scanned'][:5])}")
        print(f"      executed {len(ran)}/{len(planned)}: {', '.join(ran[:8])}"
              f"{'…' if len(ran) > 8 else ''}")
    print()


def report_coherence(snapshot: dict):
    print("=" * 100)
    print("3. COHERENCE — contradictory states the Go invariant cannot retroactively repair")
    print("=" * 100)
    drift = []
    for name, wf in sorted(snapshot["workflows"].items()):
        for fp, f in wf["findings"].items():
            if f["issue_kind"] == "harness_issue" and f["status"] == "queued_for_engineering":
                drift.append((name, fp[:8], f["module"], f["label"]))
    if not drift:
        print("  None. Every harness_issue carries a status the workflow can actually act on.")
    else:
        print("  harness_issue + queued_for_engineering — the workflow cannot repair the boundary it")
        print("  queued for repair, so every pass rediscovers and re-defers it. New writes are now")
        print("  rejected in RecordPulseFindingDispositionsTx; these predate that guard and need")
        print("  re-dispositioning to external_action_required (with owner/reason/reopen) by hand or Pulse.")
        print()
        for wf_name, fp, module, label in drift:
            print(f"    {wf_name:<22} {fp}  {module:<20} {label[:60]}")
    print()


def report_stranded_gate_decisions(snapshot: dict, stale_after_hours: float = 2.0):
    """Gate recorded a module as due, and nothing has resolved it since.

    Found 2026-08-10: a social-media Gate pass recorded workflow_review=due with
    a real P0 (two graders disagreeing on the same public content), then the
    session's turn ended. Nothing else was ever going to pick it up — Gate's own
    contract forbids it from launching reviewers itself, and the architecture
    has "no scheduler-launched residual or recovery Fixer" by design, so a
    dropped handoff here is silent by construction. It sat unactioned for hours
    until someone happened to ask. A same-night cron-triggered run on a
    different workflow (upwork) completed its own due->result cycle normally,
    so this is not a universal failure of the mechanism — root cause not yet
    isolated (manual vs cron trigger path is the leading, unconfirmed lead).

    A "due" decision is stranded when last_gate_decision='due' and last_result
    is empty (no Review+Fix pass has ever written a result for it) and enough
    time has passed that a normal review would have finished. Short-lived empty
    results are not flagged -- a review pass legitimately takes time.
    """
    print("=" * 100)
    print("3b. STRANDED GATE DECISIONS — Gate said a module is due; nothing resolved it")
    print("=" * 100)
    now = datetime.datetime.now(datetime.timezone.utc)
    stranded = []
    for name, wf in sorted(snapshot["workflows"].items()):
        for module, state in wf.get("module_state", {}).items():
            if state["gate_decision"] != "due" or state["result"]:
                continue
            checked_at = state["checked_at"]
            if not checked_at:
                continue
            try:
                ts = datetime.datetime.strptime(checked_at, "%Y-%m-%dT%H:%M:%SZ").replace(
                    tzinfo=datetime.timezone.utc)
            except ValueError:
                continue
            age_hours = (now - ts).total_seconds() / 3600.0
            if age_hours >= stale_after_hours:
                stranded.append((name, module, state["pulse_run_id"], checked_at, age_hours))
    if not stranded:
        print("  None. Every 'due' Gate decision either has a result or is still recent enough to be in progress.")
    else:
        print(f"  {len(stranded)} module(s) marked due by Gate with no Review+Fix result after "
              f"{stale_after_hours:.0f}+ hours. Nothing will pick these up automatically --")
        print("  the architecture has no scheduler-launched recovery Fixer by design. Check manually or")
        print("  wait for the workflow's next scheduled trigger to run Gate again.")
        print()
        for wf_name, module, run_id, checked_at, age in sorted(stranded, key=lambda r: -r[4]):
            print(f"    {wf_name:<22} {module:<18} due since {checked_at} ({age:.1f}h ago)  run={run_id}")
    print()


def report_untriaged(snapshot: dict, known_ids: set):
    print("=" * 100)
    print("4. UNTRIAGED PLATFORM CANDIDATES — external_action_required citing no PLAT-NNN")
    print("=" * 100)
    total = untriaged = 0
    for name, wf in sorted(snapshot["workflows"].items()):
        rows = [(fp, f) for fp, f in wf["findings"].items()
                if f["status"] == "external_action_required"]
        if not rows:
            continue
        print(f"--- {name} ({len(rows)}) ---")
        for fp, f in sorted(rows, key=lambda kv: kv[1]["module"]):
            total += 1
            linked = bool(f["plat_ids"])
            if not linked:
                untriaged += 1
            tag = "linked:" + ",".join(f["plat_ids"]) if linked else "UNTRIAGED"
            print(f"  [{tag:<20}] {fp[:8]}  {f['module']:<22} {f['label'][:62]}")
        print()
    print(f"  {total} external_action_required findings; MAY_NEED_ATTENTION={untriaged}")
    if untriaged:
        print("  An UNTRIAGED row is not automatically a new ticket — it may be a fresh instance of an")
        print("  existing one in different words. Read it against docs/bugs/pulse_platform/index.md")
        print("  before filing a new PLAT-NNN.")
    print()


def report_cohort(snapshot: dict, baseline: dict):
    """The measurement that survives reviewer lenses being disabled."""
    print("=" * 100)
    print("5. COHORT DIFF vs BASELINE — did the findings that were open at baseline actually close?")
    print("=" * 100)
    print(f"  baseline captured: {baseline.get('captured_at', 'unknown')}")
    print(f"  current:           {snapshot.get('captured_at', 'unknown')}")
    print()
    print(f"{'workflow':<24}{'cohort':>8}{'closed':>8}{'moved':>8}{'stuck':>8}{'untested':>10}"
          f"{'run-gtd':>9}{'rg-closed':>11}{'new':>6}")
    print("-" * 110)
    grand = collections.Counter()
    for name, wf in sorted(snapshot["workflows"].items()):
        base_wf = baseline.get("workflows", {}).get(name)
        if not base_wf:
            continue
        base_active = {fp: f for fp, f in base_wf["findings"].items()
                       if f["status"] not in CLOSED_STATUSES}
        if not base_active:
            continue
        now = wf["findings"]
        cov = wf.get("coverage") or {}
        planned = set(cov.get("plan_steps", []))
        ran = set(cov.get("executed", {}))
        closed = moved = stuck = untested = 0
        rg_total = rg_closed = 0
        for fp, bf in base_active.items():
            cur = now.get(fp)
            was_run_gated = bf["status"] in RUN_GATED_STATUSES
            if was_run_gated:
                rg_total += 1
            if cur is None:
                # Gone entirely — merged into another fingerprint. Count as moved.
                moved += 1
                continue
            if cur["status"] in CLOSED_STATUSES:
                closed += 1
                if was_run_gated:
                    rg_closed += 1
            elif cur["status"] != bf["status"]:
                moved += 1
            else:
                # The split that keeps this honest: a finding whose step never
                # ran is untested, not a failed fix.
                module = cur.get("module", "")
                if module in planned and module not in ran:
                    untested += 1
                else:
                    stuck += 1
        new = sum(1 for fp in now if fp not in base_wf["findings"])
        print(f"{name:<24}{len(base_active):>8}{closed:>8}{moved:>8}{stuck:>8}{untested:>10}"
              f"{rg_total:>9}{rg_closed:>11}{new:>6}")
        grand["cohort"] += len(base_active); grand["closed"] += closed
        grand["moved"] += moved; grand["stuck"] += stuck; grand["untested"] += untested
        grand["rg"] += rg_total; grand["rg_closed"] += rg_closed; grand["new"] += new
    print("-" * 100)
    print(f"{'TOTAL':<24}{grand['cohort']:>8}{grand['closed']:>8}{grand['moved']:>8}"
          f"{grand['stuck']:>8}{grand['untested']:>10}{grand['rg']:>9}"
          f"{grand['rg_closed']:>11}{grand['new']:>6}")
    print()
    print("  stuck    = same status, and its step DID run — the fix genuinely did not work")
    print("  untested = same status, but its step never ran — no verdict is available yet")
    print()
    print(f"  PRIMARY SIGNAL: of {grand['rg']} findings that were run-gated at baseline, "
          f"{grand['rg_closed']} closed ({pct(grand['rg_closed'], grand['rg']).strip()}).")
    print("  Those are the ones PLAT-054 directly unblocks. If this stays near zero, producing runs")
    print("  are still not completing and the watchdog fix did not work — regardless of totals.")
    print()

    # Filing rate by module: a drop here may just be a disabled lens.
    new_by_module = collections.Counter()
    for name, wf in snapshot["workflows"].items():
        base_wf = baseline.get("workflows", {}).get(name)
        if not base_wf:
            continue
        for fp, f in wf["findings"].items():
            if fp not in base_wf["findings"]:
                new_by_module[f["module"] or "(step-raised)"] += 1
    if new_by_module:
        print("  New findings filed since baseline, by module (a lens disabled at the Gate files zero —")
        print("  that is suppression, not improvement):")
        for module, count in new_by_module.most_common(12):
            retired = "  [retired module name]" if module in RETIRED_MODULES else ""
            print(f"    {count:4d}  {module}{retired}")
    else:
        print("  No new findings filed since baseline.")
    print()


def main():
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--workflow-root", default=None,
                        help="dir containing one subdir per workflow "
                             "(default: workspace-docs/Workflow under the repo root)")
    parser.add_argument("--json", dest="json_out", default=None,
                        help="write a snapshot here (use as a later --baseline)")
    parser.add_argument("--baseline", default=None,
                        help="a snapshot to diff against; prints the cohort report")
    parser.add_argument("--min-category", type=int, default=2,
                        help="minimum findings for a category row (default 2)")
    parser.add_argument("--disabled-modules",
                        default=",".join(sorted(DEFAULT_DISABLED_MODULES)),
                        help="comma-separated modules currently suppressed at the Gate; their "
                             "findings cannot close because nothing re-reviews them "
                             "(pass '' when all four lenses are enabled again)")
    parser.add_argument("--section", default="all",
                        choices=["all", "loop", "category", "coverage", "coherence",
                                 "stranded", "untriaged", "cohort"])
    parser.add_argument("--stranded-hours", type=float, default=2.0,
                        help="how long a Gate 'due' decision must sit with no result before "
                             "it's reported as stranded (default 2)")
    args = parser.parse_args()

    global DISABLED_MODULES
    DISABLED_MODULES = {m.strip() for m in args.disabled_modules.split(",") if m.strip()}

    repo_root = Path(__file__).resolve().parent.parent
    workflow_root = (Path(args.workflow_root) if args.workflow_root
                     else repo_root / "workspace-docs" / "Workflow")
    if not workflow_root.is_dir():
        print(f"workflow root not found: {workflow_root}", file=sys.stderr)
        return 1

    snapshot = {
        "captured_at": datetime.datetime.now(datetime.timezone.utc)
                               .strftime("%Y-%m-%dT%H:%M:%SZ"),
        "workflows": {},
    }
    for db_path in sorted(workflow_root.glob("*/db/db.sqlite")):
        name = db_path.parent.parent.name
        wf = scan_workflow(db_path)
        if wf["findings"] or wf["events"]:
            wf["events"] = dict(wf["events"])
            wf["coverage"] = scan_step_coverage(db_path.parent.parent)
            snapshot["workflows"][name] = wf

    if not snapshot["workflows"]:
        print(f"No Pulse-enabled workflow databases found under {workflow_root}")
        return 0

    want = args.section
    if want in ("all", "loop"):
        report_loop_health(snapshot)
    if want in ("all", "category"):
        report_categories(snapshot, args.min_category)
    if want in ("all", "coverage"):
        report_step_coverage(snapshot)
    if want in ("all", "coherence"):
        report_coherence(snapshot)
    if want in ("all", "stranded"):
        report_stranded_gate_decisions(snapshot, args.stranded_hours)
    if want in ("all", "untriaged"):
        report_untriaged(snapshot, load_register_ids(repo_root))
    if args.baseline and want in ("all", "cohort"):
        with open(args.baseline) as fh:
            report_cohort(snapshot, json.load(fh))
    elif want == "cohort":
        print("--section cohort requires --baseline", file=sys.stderr)
        return 1

    if args.json_out:
        with open(args.json_out, "w") as fh:
            json.dump(snapshot, fh, indent=1, sort_keys=True)
        print(f"snapshot written: {args.json_out}  "
              f"({len(snapshot['workflows'])} workflows, "
              f"{sum(len(w['findings']) for w in snapshot['workflows'].values())} findings)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
