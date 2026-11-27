"""Unit tests for nightly full server test runner and new-failure diff reporter (#4340)."""
import fcntl
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

DEPLOY_COMMON = Path(__file__).resolve().parent
sys.path.insert(0, str(DEPLOY_COMMON))

import nightly_tests_report as report  # noqa: E402

RUNNER_SCRIPT = DEPLOY_COMMON / "nightly-tests.sh"
BUILD_ONCE = DEPLOY_COMMON / "build-once.sh"

FLOCK_SHIM = """#!/usr/bin/env python3
import fcntl, sys, time

args = sys.argv[1:]
nonblock = False
timeout = None
i = 0
while i < len(args):
    a = args[i]
    if a == "-n":
        nonblock = True
        i += 1
    elif a == "-w":
        timeout = float(args[i + 1])
        i += 2
    else:
        break
fd = int(args[i])
deadline = None if timeout is None else time.monotonic() + timeout
while True:
    try:
        flags = fcntl.LOCK_EX | (fcntl.LOCK_NB if (nonblock or timeout is not None) else 0)
        fcntl.flock(fd, flags)
        sys.exit(0)
    except BlockingIOError:
        if nonblock or (deadline is not None and time.monotonic() >= deadline):
            sys.exit(1)
        time.sleep(0.02)
"""


def _write_exec(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o755)


def _git(cwd: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=cwd, text=True).strip()


def _make_repo(root: Path) -> tuple[str, str]:
    remote = root / "remote.git"
    work = root / "work"
    _git(root, "init", "--bare", str(remote))
    _git(root, "clone", str(remote), str(work))
    _git(work, "config", "user.email", "test@example.com")
    _git(work, "config", "user.name", "Test")
    (work / "agent_go").mkdir(parents=True)
    (work / "agent_go" / "go.mod").write_text("module example.com/agent_go\n", encoding="utf-8")
    (work / "workspace" / "cmd" / "landlock-runner").mkdir(parents=True)
    (work / "deploy" / "common").mkdir(parents=True)
    shutil.copy2(DEPLOY_COMMON / "nightly_tests_report.py", work / "deploy" / "common" / "nightly_tests_report.py")
    _git(work, "add", ".")
    _git(work, "commit", "-m", "init")
    _git(work, "branch", "-M", "main")
    _git(work, "push", "-u", "origin", "main")
    sha = _git(work, "rev-parse", "HEAD")
    return str(remote), sha


class NightlyReportUnitTests(unittest.TestCase):
    def test_parse_collapses_parent_when_subtest_fails_and_captures_package_fail(self):
        pkg = "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server"
        other_pkg = "github.com/manishiitg/coding-agent-loop/agent_go/pkg/broken"
        events = [
            json.dumps({"Action": "pass", "Package": pkg, "Test": "TestHealthy"}),
            json.dumps({"Action": "skip", "Package": pkg, "Test": "TestSkipped"}),
            json.dumps({"Action": "output", "Package": pkg, "Test": "TestGroup/caseA", "Output": "    foo_test.go:42: want 1, got 2\n"}),
            json.dumps({"Action": "fail", "Package": pkg, "Test": "TestGroup/caseA"}),
            json.dumps({"Action": "fail", "Package": pkg, "Test": "TestGroup"}),
            json.dumps({"Action": "output", "Package": pkg, "Test": "TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages", "Output": "    crew_owner_tabs_test.go:121: Main isn't ready\n"}),
            json.dumps({"Action": "fail", "Package": pkg, "Test": "TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages"}),
            json.dumps({"Action": "fail", "Package": pkg}),
            json.dumps({"Action": "build-output", "ImportPath": other_pkg, "Output": "pkg/broken/x.go:10: undefined: nope\n"}),
            json.dumps({"Action": "build-fail", "ImportPath": other_pkg}),
        ]
        parsed = report.parse_go_test_json(events)
        self.assertEqual(parsed["passed_count"], 1)
        self.assertEqual(parsed["skipped_count"], 1)
        self.assertEqual(parsed["failed_count"], 3)
        keys = set(parsed["failures"].keys())
        self.assertEqual(
            keys,
            {
                "cmd/server::TestGroup/caseA",
                "cmd/server::TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages",
                "pkg/broken::(package)",
            },
        )
        self.assertEqual(
            parsed["failures"]["cmd/server::TestCrewOwnerTabsShareViewsKeepControlAndDeliverMessages"]["known_issue"],
            "#3410",
        )
        self.assertIn("foo_test.go:42", parsed["failures"]["cmd/server::TestGroup/caseA"]["snippet"])
        self.assertIn("undefined: nope", parsed["failures"]["pkg/broken::(package)"]["snippet"])

    def test_diff_and_comment_report_only_new_failures(self):
        run1 = {
            "sha": "1111111111111111111111111111111111111111",
            "timestamp": "2026-11-27T00:00:00Z",
            "duration_seconds": 120,
            "packages_tested": 10,
            "passed_count": 500,
            "skipped_count": 5,
            "log_path": "/srv/_builds/.nightly-tests/logs/run1.log",
            "failures": {
                "cmd/server::TestCrewSettingsChatToolsStayBoundToCrewAndCaller": {
                    "key": "cmd/server::TestCrewSettingsChatToolsStayBoundToCrewAndCaller",
                    "package": "cmd/server",
                    "test": "TestCrewSettingsChatToolsStayBoundToCrewAndCaller",
                    "known_issue": "#4238",
                    "snippet": "crew_settings_chat_tools_test.go:50: Main isn't ready",
                },
                "cmd/server::TestOldBroken": {
                    "key": "cmd/server::TestOldBroken",
                    "package": "cmd/server",
                    "test": "TestOldBroken",
                    "snippet": "old_test.go:10: boom",
                },
            },
        }

        # Baseline run: quiet unless include_baseline=True, and never leaks paths or raw output.
        baseline_diff = report.diff_runs(run1, None)
        self.assertTrue(baseline_diff["is_baseline"])
        self.assertIsNone(report.format_github_comment(baseline_diff, include_baseline=False))
        self.assertIsNone(report.format_github_issue(baseline_diff, include_baseline=False))
        baseline_issue = report.format_github_issue(baseline_diff, include_baseline=True)
        self.assertIsNotNone(baseline_issue)
        self.assertIn("initial baseline", baseline_issue["title"])
        baseline_md = baseline_issue["body"]
        self.assertIn("initial baseline recorded", baseline_md)
        self.assertIn("#4238 (1)", baseline_md)
        self.assertIn("TestOldBroken", baseline_md)
        self.assertNotIn("/srv/", baseline_md)
        self.assertNotIn("run1.log", baseline_md)
        self.assertNotIn("old_test.go:10: boom", baseline_md)

        # Next night with identical failures: completely quiet (None).
        run2 = dict(run1, sha="2222222222222222222222222222222222222222")
        same_diff = report.diff_runs(run2, run1)
        self.assertEqual(same_diff["new_failures"], [])
        self.assertIsNone(report.format_github_comment(same_diff))
        self.assertIsNone(report.format_github_issue(same_diff))

        # Night 3: TestOldBroken fixed, TestBrandNew fails -> open issue naming ONLY TestBrandNew,
        # without any build-host path, IP/address, account name, or raw test output snippet.
        run3 = {
            **run2,
            "sha": "3333333333333333333333333333333333333333",
            "log_path": "/srv/_builds/.nightly-tests/logs/secret-host.log",
            "failures": {
                "cmd/server::TestCrewSettingsChatToolsStayBoundToCrewAndCaller": run1["failures"][
                    "cmd/server::TestCrewSettingsChatToolsStayBoundToCrewAndCaller"
                ],
                "cmd/server::TestBrandNew": {
                    "key": "cmd/server::TestBrandNew",
                    "package": "cmd/server",
                    "test": "TestBrandNew",
                    "snippet": "/srv/_builds/.work/foo/bar_test.go:99: alice@192.0.2.10 expected ok",
                },
            },
        }
        reg_diff = report.diff_runs(run3, run2)
        self.assertEqual([f["test"] for f in reg_diff["new_failures"]], ["TestBrandNew"])
        self.assertEqual(reg_diff["resolved_failures"], ["cmd/server::TestOldBroken"])
        issue = report.format_github_issue(reg_diff)
        self.assertIsNotNone(issue)
        self.assertEqual(issue["title"], "Nightly server tests: TestBrandNew failed on 3333333333")
        md = issue["body"]
        self.assertIn("1 new failure(s)", md)
        self.assertIn("TestBrandNew", md)
        self.assertNotIn("TestCrewSettingsChatToolsStayBoundToCrewAndCaller", md)
        self.assertNotIn("/srv/", md)
        self.assertNotIn("secret-host.log", md)
        self.assertNotIn("192.0.2.10", md)
        self.assertNotIn("alice@", md)
        self.assertNotIn("expected ok", md)
        self.assertIn("Resolved since previous run (1): `cmd/server::TestOldBroken`", md)

        # Multiple new failures produce a plural count title.
        run4 = {
            **run3,
            "sha": "4444444444444444444444444444444444444444",
            "failures": {
                **run3["failures"],
                "pkg/foo::TestSecondNew": {
                    "key": "pkg/foo::TestSecondNew",
                    "package": "pkg/foo",
                    "test": "TestSecondNew",
                    "snippet": "raw output should never appear",
                },
            },
        }
        multi_issue = report.format_github_issue(report.diff_runs(run4, run2))
        self.assertEqual(multi_issue["title"], "Nightly server tests: 2 new failures on 4444444444")
        self.assertNotIn("raw output should never appear", multi_issue["body"])


class NightlyRunnerShellTests(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="nightly-test-"))
        self.builds = self.tmp / "builds"
        self.builds.mkdir()
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        _write_exec(self.bin / "flock", FLOCK_SHIM)
        self.remote, self.sha = _make_repo(self.tmp)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _base_env(self, fake_go_events: Path) -> dict[str, str]:
        go_log = self.tmp / "go.log"
        toolchain_bin = self.builds / ".toolchain" / "go" / "bin"
        toolchain_bin.mkdir(parents=True, exist_ok=True)
        _write_exec(
            toolchain_bin / "go",
            f"""#!/usr/bin/env bash
set -euo pipefail
echo "go $* | GOMAXPROCS=${{GOMAXPROCS:-}} | GOCACHE=${{GOCACHE:-}} | LINUX_ROOT=${{AGENTWORKS_LINUX_TEST_ROOT:-}}" >> "{go_log}"
if [[ "$1" == "test" ]]; then
  cat "{fake_go_events}"
  if grep -qE '"Action":[[:space:]]*"fail"' "{fake_go_events}"; then
    exit 1
  fi
fi
exit 0
""",
        )
        env = os.environ.copy()
        env["PATH"] = f"{self.bin}:{env['PATH']}"
        env["NIGHTLY_DISK_MIN_FREE_GIB"] = "0"
        env["NIGHTLY_DEPLOY_WAIT_SECONDS"] = "0.05"
        return env

    def test_runs_concurrently_with_all_verify_slots_locked_and_diffs_state(self):
        # Hold ALL 4 Go verify slot locks to prove nightly-tests never blocks on or takes verify slots.
        held_fds = []
        try:
            for i in range(1, 5):
                fh = open(self.builds / f".verify.lock.{i}", "w")
                fcntl.flock(fh.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                held_fds.append(fh)

            events1 = self.tmp / "events1.jsonl"
            pkg = "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server"
            events1.write_text(
                "\n".join(
                    [
                        json.dumps({"Action": "pass", "Package": pkg, "Test": "TestOne"}),
                        json.dumps({"Action": "output", "Package": pkg, "Test": "TestOld", "Output": "old_test.go:5: fail\n"}),
                        json.dumps({"Action": "fail", "Package": pkg, "Test": "TestOld"}),
                        json.dumps({"Action": "fail", "Package": pkg}),
                    ]
                )
                + "\n",
                encoding="utf-8",
            )
            env = self._base_env(events1)
            res1 = subprocess.run(
                [
                    "bash",
                    str(RUNNER_SCRIPT),
                    "--url",
                    self.remote,
                    "--sha",
                    self.sha,
                    "--builds-dir",
                    str(self.builds),
                ],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(res1.returncode, 0, res1.stderr)
            report_lines = [l for l in res1.stdout.splitlines() if l.startswith(report.REPORT_PREFIX)]
            self.assertEqual(len(report_lines), 1)
            diff1 = json.loads(report_lines[0][len(report.REPORT_PREFIX):])
            self.assertTrue(diff1["is_baseline"])
            self.assertEqual(diff1["failed_count"], 1)
            self.assertTrue(Path(diff1["log_path"]).is_file())

            # Verify isolated GOCACHE and capped GOMAXPROCS/-p/-parallel.
            go_log = (self.tmp / "go.log").read_text(encoding="utf-8")
            self.assertIn("go-build-nightly", go_log)
            self.assertIn("GOMAXPROCS=2", go_log)
            self.assertIn("-p 2 -parallel 2", go_log)

            # Second run introduces TestNew -> only TestNew appears in new_failures.
            events2 = self.tmp / "events2.jsonl"
            events2.write_text(
                "\n".join(
                    [
                        json.dumps({"Action": "pass", "Package": pkg, "Test": "TestOne"}),
                        json.dumps({"Action": "fail", "Package": pkg, "Test": "TestOld"}),
                        json.dumps({"Action": "output", "Package": pkg, "Test": "TestNew", "Output": "new_test.go:11: regressed\n"}),
                        json.dumps({"Action": "fail", "Package": pkg, "Test": "TestNew"}),
                        json.dumps({"Action": "fail", "Package": pkg}),
                    ]
                )
                + "\n",
                encoding="utf-8",
            )
            env2 = self._base_env(events2)
            res2 = subprocess.run(
                [
                    "bash",
                    str(RUNNER_SCRIPT),
                    "--url",
                    self.remote,
                    "--sha",
                    self.sha,
                    "--builds-dir",
                    str(self.builds),
                ],
                env=env2,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(res2.returncode, 0, res2.stderr)
            diff2 = json.loads(
                [l for l in res2.stdout.splitlines() if l.startswith(report.REPORT_PREFIX)][0][len(report.REPORT_PREFIX):]
            )
            self.assertFalse(diff2["is_baseline"])
            self.assertEqual([f["test"] for f in diff2["new_failures"]], ["TestNew"])
        finally:
            for fh in held_fds:
                fh.close()

    def test_single_instance_lock_and_low_disk_refusal_preserve_state(self):
        events = self.tmp / "events.jsonl"
        events.write_text("", encoding="utf-8")
        env = self._base_env(events)

        # Hold .nightly-test.lock -> second caller exits 75 immediately.
        with open(self.builds / ".nightly-test.lock", "w") as lock_fh:
            fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            res_busy = subprocess.run(
                ["bash", str(RUNNER_SCRIPT), "--url", self.remote, "--builds-dir", str(self.builds)],
                env=env,
                capture_output=True,
                text=True,
            )
        self.assertEqual(res_busy.returncode, 75)

        # Low disk floor refuses with exit 2 and does not overwrite existing state.
        state_file = self.builds / ".nightly-tests" / "last-run.json"
        state_file.parent.mkdir(parents=True, exist_ok=True)
        state_file.write_text('{"sha":"keep"}\n', encoding="utf-8")
        low_env = dict(env, NIGHTLY_DISK_MIN_FREE_GIB="9999999")
        res_disk = subprocess.run(
            ["bash", str(RUNNER_SCRIPT), "--url", self.remote, "--builds-dir", str(self.builds)],
            env=low_env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(res_disk.returncode, 2)
        self.assertEqual(state_file.read_text(encoding="utf-8"), '{"sha":"keep"}\n')

        # Active deploy lock defers until timeout (exits 2 without touching state).
        deploy_lock = self.builds / ".deploys" / "example"
        deploy_lock.mkdir(parents=True, exist_ok=True)
        try:
            dep_env = dict(env, NIGHTLY_DEPLOY_TIMEOUT_SECONDS="0.1")
            res_dep = subprocess.run(
                ["bash", str(RUNNER_SCRIPT), "--url", self.remote, "--builds-dir", str(self.builds)],
                env=dep_env,
                capture_output=True,
                text=True,
            )
            self.assertEqual(res_dep.returncode, 2)
            self.assertIn("Deferring start while a deploy is active", res_dep.stderr)
            self.assertEqual(state_file.read_text(encoding="utf-8"), '{"sha":"keep"}\n')
        finally:
            shutil.rmtree(self.builds / ".deploys", ignore_errors=True)

    def test_build_once_runs_nightly_with_idle_priority_limits(self):
        src = BUILD_ONCE.read_text(encoding="utf-8")
        self.assertIn("nightly_tests_remote()", src)
        self.assertIn("-p Nice=19", src)
        self.assertIn("-p IOSchedulingClass=idle", src)
        self.assertIn("NIGHTLY_CPU_QUOTA:-200%", src)
        self.assertIn("NIGHTLY_MEMORY_MAX:-6G", src)
        self.assertIn("--open-issue", src)
        self.assertIn("gh issue create", src)


if __name__ == "__main__":
    unittest.main()
