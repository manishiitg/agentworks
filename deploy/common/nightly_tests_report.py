#!/usr/bin/env python3
"""Parse `go test -json` output from nightly full server test runs, diff against
the previous run's baseline, and format concise GitHub issue comments reporting
only failures new since the previous run (#4340).

Pure Python 3 standard library so it runs identically on the Linux build host
and on macOS developer machines.
"""
from __future__ import annotations

import argparse
from collections import defaultdict
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
from typing import Any, Iterable

MODULE_PREFIX = "github.com/manishiitg/coding-agent-loop/agent_go/"
REPORT_PREFIX = "NIGHTLY_REPORT_JSON="

# Open known failure issues listed in #4340. Used only to annotate baseline /
# reappearing flaky failures — new-failure detection always diffs against the
# previous completed run so newly broken tests are never hidden.
KNOWN_FAILURE_ISSUES: dict[str, str] = {
    # #4303 — Seven Team tests fail on main
    "TestChatMessageSearchRechecksPrivateAndHiddenChats": "#4303",
    "TestCrewTeamActivityBridgePreviewSeenAndOwnerThread": "#4303",
    "TestCrewTeamFolderChangeKeepsStoreWithoutReupload": "#4303",
    "TestTeamExistingProjectSyncKeepsPrivateFoldersAndSkills": "#4303",
    "TestCrewTeamMemberAccessChangesOnlyOwnParticipation": "#4303",
    "TestCrewTeamFolderHTTPConflictRetryAndRevocation": "#4303",
    "TestCrewTeamMemberMacAdmittedOnlyWithTheirRecordedPair": "#4303",
    # #4019 — Three Watcher tests fail
    "TestAgentEmailWatcherRealPythonAndDurableCursor": "#4019",
    "TestPersonalGmailWatcherKeepsMailPrivateAndPinsOwnerSource": "#4019",
    "TestConditionProductCallerOwnsDetectorAndCallsOwnCrew": "#4019",
    # #3324 — Server schedule / executable / flaky timer tests
    "TestPausedCalendarQueueRetainsOccurrenceUntilResume": "#3324",
    "TestCrewExecutableUsesTrustedParentAndLiveAccess": "#3324",
    "TestCrewTimerLedgerRecordsTerminalOutcomesWithoutSecondScopeLease": "#3324",
    # #3410 — Crew owner tabs test
    "TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages": "#3410",
    # #4238 — Crew settings chat tools test
    "TestCrewSettingsChatToolsStayBoundToCrewAndCaller": "#4238",
}

MAX_SNIPPET_LINES = 8
MAX_SNIPPET_CHARS = 700
DEFAULT_HISTORY_KEEP = 14


def short_package(pkg: str) -> str:
    pkg = (pkg or "").strip()
    if pkg.startswith(MODULE_PREFIX):
        return pkg[len(MODULE_PREFIX):]
    return pkg or "(unknown)"


def known_issue_for(test_name: str) -> str | None:
    if not test_name:
        return None
    if test_name in KNOWN_FAILURE_ISSUES:
        return KNOWN_FAILURE_ISSUES[test_name]
    root = test_name.split("/", 1)[0]
    return KNOWN_FAILURE_ISSUES.get(root)


def trim_snippet(lines: list[str]) -> str:
    cleaned = [line.rstrip() for line in lines if line.strip()]
    if not cleaned:
        return ""
    # Prefer assertion / error / panic lines when output is noisy, while keeping
    # surrounding context up to MAX_SNIPPET_LINES.
    interesting = [
        line
        for line in cleaned
        if any(
            marker in line
            for marker in (
                "_test.go:",
                "FAIL:",
                "panic:",
                "Error:",
                "error:",
                "fatal:",
                "timeout",
                "want ",
                "expected ",
                "build failed",
            )
        )
    ]
    chosen = interesting[-MAX_SNIPPET_LINES:] if interesting else cleaned[-MAX_SNIPPET_LINES:]
    text = "\n".join(chosen)
    if len(text) > MAX_SNIPPET_CHARS:
        text = text[: MAX_SNIPPET_CHARS - 3].rstrip() + "..."
    return text


def parse_go_test_json(
    lines: Iterable[str],
    stderr_text: str = "",
) -> dict[str, Any]:
    """Parse `go test -json` stream into structured pass/fail/skip results."""
    pkg_events_seen: set[str] = set()
    pkg_failed: set[str] = set()
    pkg_build_failed: dict[str, list[str]] = defaultdict(list)
    pkg_output: dict[str, list[str]] = defaultdict(list)

    test_output: dict[tuple[str, str], list[str]] = defaultdict(list)
    test_passed: set[tuple[str, str]] = set()
    test_failed: set[tuple[str, str]] = set()
    test_skipped: set[tuple[str, str]] = set()
    non_json_lines: list[str] = []

    for raw in lines:
        s = raw.strip()
        if not s:
            continue
        try:
            ev = json.loads(s)
        except json.JSONDecodeError:
            non_json_lines.append(s)
            continue
        if not isinstance(ev, dict):
            continue

        action = ev.get("Action") or ""
        pkg = short_package(ev.get("Package") or ev.get("ImportPath") or "")
        test = ev.get("Test") or ""
        out = ev.get("Output") or ""

        if pkg and pkg != "(unknown)":
            pkg_events_seen.add(pkg)

        if action == "build-output":
            if out:
                pkg_build_failed[pkg].append(out)
        elif action == "build-fail":
            pkg_failed.add(pkg)
            if pkg not in pkg_build_failed:
                pkg_build_failed[pkg].append("package build failed")
        elif test:
            key = (pkg, test)
            if action == "output" and out:
                buf = test_output[key]
                buf.append(out)
                if len(buf) > 80:
                    del buf[: len(buf) - 80]
            elif action == "fail":
                test_failed.add(key)
            elif action == "pass":
                test_passed.add(key)
            elif action == "skip":
                test_skipped.add(key)
        else:
            if action == "output" and out:
                buf = pkg_output[pkg]
                buf.append(out)
                if len(buf) > 80:
                    del buf[: len(buf) - 80]
            elif action == "fail":
                pkg_failed.add(pkg)

    # Collapse parent test failures when one or more child subtests also failed,
    # so each leaf failure is tracked precisely without double-reporting.
    failed_by_pkg: dict[str, set[str]] = defaultdict(set)
    for pkg, test in test_failed:
        failed_by_pkg[pkg].add(test)

    failures: dict[str, dict[str, Any]] = {}
    for pkg, tests in sorted(failed_by_pkg.items()):
        leaf_tests = sorted(
            t for t in tests if not any(other.startswith(t + "/") for other in tests)
        )
        for test in leaf_tests:
            key = f"{pkg}::{test}"
            snippet = trim_snippet(test_output.get((pkg, test), []))
            if not snippet:
                snippet = trim_snippet(pkg_output.get(pkg, []))
            entry: dict[str, Any] = {
                "key": key,
                "package": pkg,
                "test": test,
                "snippet": snippet,
            }
            issue = known_issue_for(test)
            if issue:
                entry["known_issue"] = issue
            failures[key] = entry

    # Package-level failures without any recorded failing test (compile errors,
    # TestMain panics, package timeouts).
    for pkg in sorted(pkg_failed):
        if failed_by_pkg.get(pkg):
            continue
        key = f"{pkg}::(package)"
        snippet = trim_snippet(pkg_build_failed.get(pkg) or pkg_output.get(pkg, []))
        failures[key] = {
            "key": key,
            "package": pkg,
            "test": "(package)",
            "snippet": snippet or "package failed without individual test failure",
        }

    # If go test produced no JSON events at all but stderr/stdout had errors,
    # record a runner failure so compile/module errors are never silently dropped.
    if not pkg_events_seen and not failures and (stderr_text.strip() or non_json_lines):
        combined = non_json_lines + stderr_text.splitlines()
        failures["(runner)::(go test)"] = {
            "key": "(runner)::(go test)",
            "package": "(runner)",
            "test": "(go test)",
            "snippet": trim_snippet(combined) or "go test produced no JSON events",
        }

    return {
        "packages_tested": len(pkg_events_seen),
        "passed_count": len(test_passed),
        "skipped_count": len(test_skipped),
        "failed_count": len(failures),
        "failures": failures,
    }


def diff_runs(
    current: dict[str, Any],
    previous: dict[str, Any] | None,
) -> dict[str, Any]:
    """Compare current run against previous run's failures."""
    cur_failures: dict[str, dict[str, Any]] = current.get("failures") or {}
    if previous is None:
        all_list = [cur_failures[k] for k in sorted(cur_failures)]
        tracked = [f for f in all_list if f.get("known_issue")]
        untracked = [f for f in all_list if not f.get("known_issue")]
        return {
            "is_baseline": True,
            "previous_sha": "",
            "previous_timestamp": "",
            "sha": current.get("sha", ""),
            "timestamp": current.get("timestamp", ""),
            "duration_seconds": current.get("duration_seconds", 0),
            "packages_tested": current.get("packages_tested", 0),
            "passed_count": current.get("passed_count", 0),
            "skipped_count": current.get("skipped_count", 0),
            "failed_count": len(cur_failures),
            "log_path": current.get("log_path", ""),
            "new_failures": [],
            "resolved_failures": [],
            "baseline_tracked_failures": tracked,
            "baseline_untracked_failures": untracked,
        }

    prev_failures: dict[str, Any] = previous.get("failures") or {}
    prev_keys = set(prev_failures.keys())
    cur_keys = set(cur_failures.keys())

    new_keys = sorted(cur_keys - prev_keys)
    resolved_keys = sorted(prev_keys - cur_keys)

    return {
        "is_baseline": False,
        "previous_sha": previous.get("sha", ""),
        "previous_timestamp": previous.get("timestamp", ""),
        "sha": current.get("sha", ""),
        "timestamp": current.get("timestamp", ""),
        "duration_seconds": current.get("duration_seconds", 0),
        "packages_tested": current.get("packages_tested", 0),
        "passed_count": current.get("passed_count", 0),
        "skipped_count": current.get("skipped_count", 0),
        "failed_count": len(cur_failures),
        "log_path": current.get("log_path", ""),
        "new_failures": [cur_failures[k] for k in new_keys],
        "resolved_failures": resolved_keys,
        "baseline_tracked_failures": [],
        "baseline_untracked_failures": [],
    }


def _short_sha(sha: str) -> str:
    return sha[:10] if sha else "unknown"


def _fmt_duration(seconds: int | float) -> str:
    s = max(0, int(round(seconds)))
    if s < 60:
        return f"{s}s"
    return f"{s // 60}m {s % 60:02d}s"


def format_github_comment(diff: dict[str, Any], include_baseline: bool = False) -> str | None:
    """Format a GitHub comment for #4340.

    Returns None when there are no new failures to report (quiet when nothing
    regressed since the previous run).
    """
    sha = _short_sha(diff.get("sha", ""))
    prev_sha = _short_sha(diff.get("previous_sha", ""))
    passed = diff.get("passed_count", 0)
    failed = diff.get("failed_count", 0)
    skipped = diff.get("skipped_count", 0)
    pkgs = diff.get("packages_tested", 0)
    dur = _fmt_duration(diff.get("duration_seconds", 0))
    log_path = diff.get("log_path", "")

    if diff.get("is_baseline"):
        if not include_baseline:
            return None
        tracked = diff.get("baseline_tracked_failures") or []
        untracked = diff.get("baseline_untracked_failures") or []
        lines = [
            f"### Nightly server tests — initial baseline recorded on `{sha}`",
            "",
            f"- **Totals:** {passed} passed, {failed} failed, {skipped} skipped across {pkgs} packages ({dur})",
            f"- **Already tracked (open issues):** {len(tracked)} failure(s)",
            f"- **Untracked on baseline:** {len(untracked)} failure(s)",
        ]
        if untracked:
            lines.append("")
            lines.append("#### Untracked failures on initial baseline")
            for item in untracked:
                lines.append(f"- `{item['package']}` · `{item['test']}`")
                if item.get("snippet"):
                    first_line = item["snippet"].splitlines()[-1]
                    lines.append(f"  > `{first_line[:220]}`")
        if tracked:
            by_issue: dict[str, list[str]] = defaultdict(list)
            for item in tracked:
                by_issue[item["known_issue"]].append(item["test"])
            summary = ", ".join(f"{iss} ({len(tests)})" for iss, tests in sorted(by_issue.items()))
            lines.append("")
            lines.append(f"Tracked failures recorded in baseline: {summary}.")
        if log_path:
            lines.append("")
            lines.append(f"Build-host log: `{log_path}`")
        lines.append("")
        lines.append("Future nightly runs will comment here only when failures appear that were not failing in the previous run.")
        return "\n".join(lines) + "\n"

    new_failures = diff.get("new_failures") or []
    if not new_failures:
        return None

    resolved = diff.get("resolved_failures") or []
    commit_range = f"`{prev_sha}..{sha}`" if prev_sha != "unknown" and prev_sha != sha else f"`{sha}`"
    lines = [
        f"### Nightly server tests — {len(new_failures)} new failure(s) on `{sha}`",
        "",
        f"- **Range:** {commit_range}",
        f"- **Totals:** {passed} passed, {failed} failed ({len(new_failures)} new), {skipped} skipped across {pkgs} packages ({dur})",
        "",
        "#### New failures since previous run",
    ]
    for item in new_failures:
        known = f" *(known: {item['known_issue']})*" if item.get("known_issue") else ""
        lines.append(f"- `{item['package']}` · `{item['test']}`{known}")
        snippet = (item.get("snippet") or "").strip()
        if snippet:
            lines.append("  ```text")
            for sline in snippet.splitlines():
                lines.append(f"  {sline}")
            lines.append("  ```")
    if resolved:
        shown = ", ".join(f"`{k}`" for k in resolved[:10])
        more = f" (+{len(resolved) - 10} more)" if len(resolved) > 10 else ""
        lines.append("")
        lines.append(f"Resolved since previous run ({len(resolved)}): {shown}{more}")
    if log_path:
        lines.append("")
        lines.append(f"Full failing test log on build host: `{log_path}`")
    return "\n".join(lines) + "\n"


def atomic_write_json(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + f".tmp.{os.getpid()}")
    tmp.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(tmp, path)


def prune_history(history_dir: Path, keep: int) -> None:
    if keep <= 0 or not history_dir.is_dir():
        return
    files = sorted(
        (p for p in history_dir.iterdir() if p.is_file() and p.suffix == ".json"),
        key=lambda p: p.name,
        reverse=True,
    )
    for old in files[keep:]:
        try:
            old.unlink()
        except OSError:
            pass


def cmd_parse_and_diff(args: argparse.Namespace) -> int:
    events_path = Path(args.events)
    stderr_text = ""
    if args.stderr and Path(args.stderr).is_file():
        stderr_text = Path(args.stderr).read_text(encoding="utf-8", errors="replace")

    with events_path.open("r", encoding="utf-8", errors="replace") as fh:
        parsed = parse_go_test_json(fh, stderr_text=stderr_text)

    now_iso = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    current = {
        "sha": args.sha,
        "timestamp": now_iso,
        "duration_seconds": int(round(float(args.duration))),
        "log_path": args.log_path or "",
        **parsed,
    }

    state_path = Path(args.state)
    previous = None
    if state_path.is_file():
        try:
            previous = json.loads(state_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            previous = None

    diff = diff_runs(current, previous)

    if not args.no_save_state:
        atomic_write_json(state_path, current)
        if args.history_dir:
            hist_dir = Path(args.history_dir)
            stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
            atomic_write_json(hist_dir / f"{stamp}-{_short_sha(args.sha)}.json", current)
            prune_history(hist_dir, args.history_keep)

    print(REPORT_PREFIX + json.dumps(diff, sort_keys=True))
    return 0


def cmd_format_comment(args: argparse.Namespace) -> int:
    raw = sys.stdin.read() if args.report_json == "-" else Path(args.report_json).read_text(encoding="utf-8")
    raw = raw.strip()
    if raw.startswith(REPORT_PREFIX):
        raw = raw[len(REPORT_PREFIX):]
    diff = json.loads(raw)
    comment = format_github_comment(diff, include_baseline=args.include_baseline)
    if comment:
        sys.stdout.write(comment)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Nightly server test parser and diff reporter")
    sub = parser.add_subparsers(dest="command", required=True)

    p_parse = sub.add_parser("parse-and-diff")
    p_parse.add_argument("--events", required=True, help="Path to go test -json output file")
    p_parse.add_argument("--stderr", default="", help="Optional path to go test stderr file")
    p_parse.add_argument("--state", required=True, help="Path to previous/current state JSON")
    p_parse.add_argument("--sha", required=True, help="40-hex commit SHA tested")
    p_parse.add_argument("--duration", default="0", help="Run duration in seconds")
    p_parse.add_argument("--log-path", default="", help="Kept build-host log path")
    p_parse.add_argument("--history-dir", default="", help="Optional directory for per-run snapshots")
    p_parse.add_argument("--history-keep", type=int, default=DEFAULT_HISTORY_KEEP)
    p_parse.add_argument("--no-save-state", action="store_true")
    p_parse.set_defaults(func=cmd_parse_and_diff)

    p_fmt = sub.add_parser("format-comment")
    p_fmt.add_argument("--report-json", default="-", help="Path to diff JSON or '-' for stdin")
    p_fmt.add_argument("--include-baseline", action="store_true")
    p_fmt.set_defaults(func=cmd_format_comment)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
