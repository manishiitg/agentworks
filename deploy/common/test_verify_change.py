"""Unit tests for deploy/common/verify-change.sh speedups (#4281):
- 4 default Go verify slots (throttling to 1 during active deploys)
- Frontend-only slot isolation (no Go slot starvation, deploy-time throttling preserved)
- LRU GOCACHE trimming (oldest-first to target size instead of full wipe)
- Cached frontend/node_modules keyed by package-lock.json hash with isolated .tmp/.vite and pruning
- Non-recursive cache ownership (no chown -R on $CACHE)
- Parallel Go + frontend checks with clean failure logs and exit codes
"""
import fcntl
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

SCRIPT = Path(__file__).with_name("verify-change.sh")
BUILD_ONCE = Path(__file__).with_name("build-once.sh")

# Portable flock shim for macOS test runs where util-linux flock is absent.
FLOCK_SHIM = """#!/usr/bin/env python3
import fcntl, os, sys, time

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

FAKE_GO = """#!/usr/bin/env bash
set -euo pipefail
log="${FAKE_LOG_DIR:?}/go.log"
echo "go $* (cwd=$PWD GOOS=${GOOS:-} GOARCH=${GOARCH:-} GOCACHE=${GOCACHE:-})" >> "$log"
if [[ "$1" == "list" && -n "${FAKE_CLI_DEP_RELS:-}" ]]; then
  repo_root="$(cd "$PWD/.." && pwd)"
  for rel in $FAKE_CLI_DEP_RELS; do
    echo "$repo_root/$rel"
  done
  exit 0
fi
if [[ -n "${FAKE_DARWIN_BARRIER_DIR:-}" && "$1" == "build" && "${GOOS:-}" == "darwin" ]]; then
  arch="${GOARCH:-}"
  touch "$FAKE_DARWIN_BARRIER_DIR/$arch.ready"
  other="amd64"
  [[ "$arch" == "amd64" ]] && other="arm64"
  for _ in $(seq 1 100); do
    [[ -f "$FAKE_DARWIN_BARRIER_DIR/$other.ready" ]] && break
    sleep 0.05
  done
  [[ -f "$FAKE_DARWIN_BARRIER_DIR/$other.ready" ]] || { echo "darwin $arch did not overlap with $other" >&2; exit 1; }
fi
if [[ -n "${FAKE_BARRIER_DIR:-}" && "$1" == "build" && "${2:-}" == "./..." ]]; then
  touch "$FAKE_BARRIER_DIR/go.ready"
  for _ in $(seq 1 100); do
    [[ -f "$FAKE_BARRIER_DIR/fe.ready" ]] && break
    sleep 0.05
  done
  [[ -f "$FAKE_BARRIER_DIR/fe.ready" ]] || { echo "Go did not overlap with frontend" >&2; exit 1; }
fi
if [[ "${FAKE_GO_FAIL:-0}" == "1" && "$1" == "build" ]]; then
  echo "--- FAIL: TestFakeGoBuild"
  echo "simulated go build failure"
  exit 1
fi
exit 0
"""

FAKE_NODE = """#!/usr/bin/env bash
if [[ "${1:-}" == "-v" ]]; then
  echo "${FAKE_NODE_VERSION:-v22.22.1}"
  exit 0
fi
exit 0
"""

FAKE_NPM = """#!/usr/bin/env bash
set -euo pipefail
log="${FAKE_LOG_DIR:?}/npm.log"
echo "npm $* (cwd=$PWD)" >> "$log"
if [[ "$1" == "ci" ]]; then
  mkdir -p node_modules/.bin node_modules/fake-pkg
  echo "pkg" > node_modules/fake-pkg/index.js
  echo "#!/bin/sh" > node_modules/.bin/tsc
  chmod +x node_modules/.bin/tsc
  exit 0
fi
if [[ "$1" == "run" && "${2:-}" == "check:theme-contract" ]]; then
  if [[ -n "${FAKE_BARRIER_DIR:-}" ]]; then
    touch "$FAKE_BARRIER_DIR/fe.ready"
    for _ in $(seq 1 100); do
      [[ -f "$FAKE_BARRIER_DIR/go.ready" ]] && break
      sleep 0.05
    done
    [[ -f "$FAKE_BARRIER_DIR/go.ready" ]] || { echo "Frontend did not overlap with Go" >&2; exit 1; }
  fi
  if [[ "${FAKE_FE_FAIL:-0}" == "1" ]]; then
    echo "FAIL theme contract mismatch"
    exit 1
  fi
  exit 0
fi
exit 0
"""

FAKE_NPX = """#!/usr/bin/env bash
set -euo pipefail
log="${FAKE_LOG_DIR:?}/npx.log"
echo "npx $* (cwd=$PWD)" >> "$log"
if [[ "$1" == "tsc" ]]; then
  # Verify node_modules is a real writable dir so tsc -b writes isolated .tmp/tsbuildinfo.
  mkdir -p node_modules/.tmp
  echo "buildinfo-$$" > node_modules/.tmp/tsconfig.app.tsbuildinfo
  if [[ "${FAKE_TSC_FAIL:-0}" == "1" ]]; then
    echo "error TS2322: simulated type error"
    exit 1
  fi
fi
exit 0
"""


def git(cwd, *args):
    return subprocess.run(
        ["git", *args],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "GIT_AUTHOR_NAME": "tester",
            "GIT_AUTHOR_EMAIL": "tester@example.com",
            "GIT_COMMITTER_NAME": "tester",
            "GIT_COMMITTER_EMAIL": "tester@example.com",
        },
    ).stdout.strip()


def hold_lock(path: Path):
    f = open(path, "a+")
    fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    return f


class VerifyChangeTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self.builds = self.root / "builds"
        self.builds.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.logs = self.root / "fake-logs"
        self.logs.mkdir()

        for name, body in (
            ("node", FAKE_NODE),
            ("npm", FAKE_NPM),
            ("npx", FAKE_NPX),
        ):
            p = self.bin / name
            p.write_text(body)
            p.chmod(0o755)
        # verify-change.sh prepends $BUILDS/.toolchain/go/bin before /usr/local/go/bin, so place fake go there.
        go_bin = self.builds / ".toolchain/go/bin"
        go_bin.mkdir(parents=True)
        (go_bin / "go").write_text(FAKE_GO)
        (go_bin / "go").chmod(0o755)
        if shutil.which("flock") is None:
            p = self.bin / "flock"
            p.write_text(FLOCK_SHIM)
            p.chmod(0o755)

        # Create a bare origin repo with main containing deploy/common/verify-change.sh, agent_go, and frontend.
        self.origin = self.root / "origin.git"
        git(self.root, "init", "-q", "--bare", str(self.origin))
        self.seed = self.root / "seed"
        self.seed.mkdir()
        git(self.seed, "init", "-q", "-b", "main")
        (self.seed / "deploy/common").mkdir(parents=True)
        shutil.copy(SCRIPT, self.seed / "deploy/common/verify-change.sh")
        (self.seed / "agent_go/cmd/server").mkdir(parents=True)
        (self.seed / "agent_go/cmd/server/main.go").write_text("package main\nfunc main() {}\n")
        (self.seed / "frontend").mkdir(parents=True)
        (self.seed / "frontend/package.json").write_text('{"name":"frontend"}\n')
        (self.seed / "frontend/package-lock.json").write_text('{"lockfileVersion":3,"v":1}\n')
        (self.seed / "frontend/src").mkdir(parents=True)
        (self.seed / "frontend/src/App.tsx").write_text("export const App = () => null;\n")
        git(self.seed, "add", "-A")
        git(self.seed, "commit", "-q", "-m", "initial main")
        git(self.seed, "remote", "add", "origin", str(self.origin))
        git(self.seed, "push", "-q", "origin", "main")
        self.main_sha = git(self.seed, "rev-parse", "HEAD")

    def make_commit(self, files: dict, message: str = "test change") -> str:
        git(self.seed, "checkout", "-q", "--detach", self.main_sha)
        for rel, content in files.items():
            p = self.seed / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content)
        git(self.seed, "add", "-A")
        git(self.seed, "commit", "-q", "-m", message)
        sha = git(self.seed, "rev-parse", "HEAD")
        git(self.seed, "push", "-q", "origin", f"HEAD:refs/heads/test-{sha[:8]}")
        return sha

    def run_verify(self, sha: str, extra_env: dict = None):
        env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "BUILD_DISK_MIN_FREE_GIB": "0",
            "FAKE_LOG_DIR": str(self.logs),
            "VERIFY_SLOT_WAIT_SECONDS": "0.05",
            "VERIFY_SLOT_TIMEOUT_SECONDS": "0.2",
        }
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            ["bash", str(SCRIPT), "--url", str(self.origin), "--sha", sha, "--builds-dir", str(self.builds)],
            capture_output=True,
            text=True,
            env=env,
        )

    def test_bash_syntax_and_no_recursive_cache_chown(self):
        subprocess.run(["bash", "-n", str(SCRIPT)], check=True)
        subprocess.run(["bash", "-n", str(BUILD_ONCE)], check=True)
        source = SCRIPT.read_text()
        self.assertNotIn('chown -R "$USER_AS:$GROUP_AS" "$WORK" "$CACHE"', source)
        self.assertNotIn('chown -R "$USER_AS:$GROUP_AS" "$CACHE"', source)
        self.assertIn('chown -R "$USER_AS:$GROUP_AS" "$WORK"', source)
        self.assertIn('install -d -o "$user" -g "$group"', source)

    def test_default_four_go_slots_and_deploy_throttles_to_one(self):
        sha = self.make_commit({"agent_go/cmd/server/main.go": "package main\nfunc main() { _ = 1 }\n"})
        # Hold slots 1, 2, 3 -> slot 4 must still be acquired immediately.
        held = [hold_lock(self.builds / f".verify.lock.{n}") for n in (1, 2, 3)]
        try:
            res = self.run_verify(sha)
            self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
            self.assertIn("Acquired verify slot 4", res.stdout)
            self.assertIn("VERIFY: PASSED", res.stdout)
            self.assertTrue((self.builds / ".verify-cache/go-build-4").is_dir())

            # Hold slot 4 too -> 5th Go verify must wait and time out.
            held.append(hold_lock(self.builds / ".verify.lock.4"))
            res_full = self.run_verify(sha)
            self.assertNotEqual(res_full.returncode, 0)
            self.assertIn("Waiting for a free go verify slot (4 in use)", res_full.stdout)
            self.assertIn("No free verify slot", res_full.stderr)
        finally:
            for f in held:
                f.close()

        # Dotfiles inside .deploys (e.g. .last-*, .queue-*) must NOT throttle to 1 slot.
        deploys = self.builds / ".deploys"
        deploys.mkdir()
        (deploys / ".last-example").write_text(sha)
        (deploys / ".queue-example").mkdir()
        lock1 = hold_lock(self.builds / ".verify.lock.1")
        try:
            res_dot = self.run_verify(sha)
            self.assertEqual(res_dot.returncode, 0, res_dot.stdout + res_dot.stderr)
            self.assertIn("Acquired verify slot 2", res_dot.stdout)

            # Active deploy lock (non-dot entry) MUST throttle to 1 slot -> blocked while slot 1 is held.
            (deploys / "example-server").mkdir()
            res_deploy = self.run_verify(sha)
            self.assertNotEqual(res_deploy.returncode, 0)
            self.assertIn("Waiting for a free go verify slot (1 in use)", res_deploy.stdout)
        finally:
            lock1.close()

    def test_frontend_only_uses_frontend_slot_without_blocking_on_go_slots(self):
        sha = self.make_commit({"frontend/src/App.tsx": "export const App = () => 1;\n"})
        # Hold ALL 4 Go slots.
        held = [hold_lock(self.builds / f".verify.lock.{n}") for n in (1, 2, 3, 4)]
        try:
            res = self.run_verify(sha)
            self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
            self.assertIn("Acquired verify slot frontend-1", res.stdout)
            self.assertIn("VERIFY: PASSED", res.stdout)
            # No Go cache dir should be created for a frontend-only run.
            for n in (1, 2, 3, 4):
                self.assertFalse((self.builds / f".verify-cache/go-build-{n}").exists())

            # During an active deploy, frontend-only must also throttle to slot 1 (.verify.lock.1).
            (self.builds / ".deploys/example-server").mkdir(parents=True)
            res_deploy = self.run_verify(sha)
            self.assertNotEqual(res_deploy.returncode, 0)
            self.assertIn("Waiting for a free frontend verify slot (1 in use)", res_deploy.stdout)
        finally:
            for f in held:
                f.close()

    def test_gocache_lru_trim_oldest_first(self):
        source = SCRIPT.read_text()
        trim_code = source.split("<<'GOCACHE_TRIM'\n", 1)[1].split("\nGOCACHE_TRIM", 1)[0]
        cache_dir = self.root / "gocache"
        (cache_dir / "00").mkdir(parents=True)
        (cache_dir / "ff").mkdir(parents=True)
        (cache_dir / "README").write_text("keep readme")
        (cache_dir / "trim.txt").write_text("keep trim")

        # 5 files of 1 MiB each = 5 MiB total; oldest first: f1, f2, f3, f4, f5.
        base_time = time.time() - 1000
        files = []
        for idx in range(1, 6):
            p = cache_dir / ("00" if idx <= 3 else "ff") / f"entry-{idx}-d"
            p.write_bytes(b"x" * (1024 * 1024))
            ts = base_time + idx * 100
            os.utime(p, (ts, ts))
            files.append(p)

        # Below cap (6 MiB): nothing removed.
        res_noop = subprocess.run(["python3", "-c", trim_code, str(cache_dir), "6", "3"], capture_output=True, text=True, check=True)
        self.assertEqual(res_noop.stderr, "")
        self.assertTrue(all(p.exists() for p in files))

        # Over cap (4 MiB max, 2 MiB target): oldest 3 removed, newest 2 + README/trim.txt preserved.
        res_trim = subprocess.run(["python3", "-c", trim_code, str(cache_dir), "4", "2"], capture_output=True, text=True, check=True)
        self.assertIn("[GOCACHE_TRIM]", res_trim.stderr)
        self.assertIn("removed=3", res_trim.stderr)
        self.assertFalse(files[0].exists())
        self.assertFalse(files[1].exists())
        self.assertFalse(files[2].exists())
        self.assertTrue(files[3].exists())
        self.assertTrue(files[4].exists())
        self.assertTrue((cache_dir / "README").exists())
        self.assertTrue((cache_dir / "trim.txt").exists())

    def test_frontend_node_modules_cache_hit_miss_isolation_and_pruning(self):
        lock_v1 = '{"lockfileVersion":3,"v":1}\n'
        hash_v1 = hashlib.sha256(lock_v1.encode()).hexdigest()
        sha1 = self.make_commit({"frontend/src/App.tsx": "export const App = () => 2;\n"})

        # Run 1: cache miss -> runs npm ci once and populates cache.
        res1 = self.run_verify(sha1)
        self.assertEqual(res1.returncode, 0, res1.stdout + res1.stderr)
        self.assertIn(f"frontend node_modules cache miss ({hash_v1[:12]})", res1.stdout)
        npm_log_1 = (self.logs / "npm.log").read_text()
        self.assertEqual(npm_log_1.count("npm ci "), 1)
        # Ensure tsc -b wrote .tmp inside $WORK, NOT inside the shared cache!
        cached_nm = self.builds / ".verify-cache/node_modules" / hash_v1 / "node_modules"
        self.assertTrue((cached_nm / "fake-pkg/index.js").is_file())
        self.assertFalse((cached_nm / ".tmp").exists())

        # Run 2: same lockfile -> cache hit, npm ci NOT called again.
        (self.logs / "npm.log").write_text("")
        res2 = self.run_verify(sha1)
        self.assertEqual(res2.returncode, 0, res2.stdout + res2.stderr)
        self.assertIn(f"frontend node_modules cache hit ({hash_v1[:12]})", res2.stdout)
        npm_log_2 = (self.logs / "npm.log").read_text()
        self.assertEqual(npm_log_2.count("npm ci "), 0)

        # Seed two older cached hashes: one very old (should be pruned) and one recent (protected by min_prune_age).
        nm_root = self.builds / ".verify-cache/node_modules"
        old_hash = "a" * 64
        recent_hash = "b" * 64
        for h, age in ((old_hash, 7200), (recent_hash, 10)):
            d = nm_root / h / "node_modules"
            d.mkdir(parents=True)
            ready = nm_root / h / ".verify-ready"
            ready.write_text("v22.22.1\n")
            ts = time.time() - age
            os.utime(ready, (ts, ts))

        # Run 3 with new lockfile (keep=2, min_prune_age=1800): old_hash pruned, recent_hash and hash_v1 kept?
        # Wait: entries sorted by mtime desc: [hash_v2 (idx 0), recent_hash (idx 1), hash_v1 (idx 2, age<1800), old_hash (idx 3, age=7200)].
        lock_v2 = '{"lockfileVersion":3,"v":2}\n'
        hash_v2 = hashlib.sha256(lock_v2.encode()).hexdigest()
        sha2 = self.make_commit({"frontend/package-lock.json": lock_v2})
        res3 = self.run_verify(sha2, {"VERIFY_NODE_MODULES_KEEP": "2", "VERIFY_NODE_MODULES_PRUNE_AGE_SECONDS": "1800"})
        self.assertEqual(res3.returncode, 0, res3.stdout + res3.stderr)
        self.assertIn(f"frontend node_modules cache miss ({hash_v2[:12]})", res3.stdout)
        self.assertIn(f"[NODE_MODULES_CACHE] pruned={old_hash[:12]}", res3.stderr)
        self.assertFalse((nm_root / old_hash).exists())
        self.assertTrue((nm_root / recent_hash).exists())
        self.assertTrue((nm_root / hash_v2).exists())

    def test_parallel_go_and_frontend_checks_and_failure_logs(self):
        sha = self.make_commit({
            "agent_go/cmd/server/main.go": "package main\nfunc main() { _ = 2 }\n",
            "frontend/src/App.tsx": "export const App = () => 3;\n",
        })
        # Rendezvous barrier: Go and frontend each wait for the other's ready file, proving true concurrency.
        barrier = self.root / "barrier"
        barrier.mkdir()
        res = self.run_verify(sha, {"FAKE_BARRIER_DIR": str(barrier)})
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        self.assertIn("Running Go and frontend checks in parallel", res.stdout)
        self.assertIn("--- frontend checks ---", res.stdout)
        self.assertIn("VERIFY: PASSED", res.stdout)
        self.assertTrue((barrier / "go.ready").is_file())
        self.assertTrue((barrier / "fe.ready").is_file())

        # Go fails, frontend passes.
        res_go_fail = self.run_verify(sha, {"FAKE_GO_FAIL": "1"})
        self.assertNotEqual(res_go_fail.returncode, 0)
        self.assertIn("TestFakeGoBuild", res_go_fail.stderr)
        self.assertIn("VERIFY: FAILED at: go build ./...", res_go_fail.stderr)
        self.assertIn("--- frontend checks ---", res_go_fail.stdout)

        # Go passes, frontend fails.
        res_fe_fail = self.run_verify(sha, {"FAKE_FE_FAIL": "1"})
        self.assertNotEqual(res_fe_fail.returncode, 0)
        self.assertIn("--- frontend checks (failed) ---", res_fe_fail.stderr)
        self.assertIn("FAIL theme contract mismatch", res_fe_fail.stderr)
        self.assertIn("VERIFY: FAILED at: semantic theme contract", res_fe_fail.stderr)

        # Both fail -> both failure diagnostics preserved cleanly.
        res_both = self.run_verify(sha, {"FAKE_GO_FAIL": "1", "FAKE_TSC_FAIL": "1"})
        self.assertNotEqual(res_both.returncode, 0)
        self.assertIn("TestFakeGoBuild", res_both.stderr)
        self.assertIn("TS2322", res_both.stderr)
        self.assertIn("VERIFY: FAILED at: Go and frontend checks", res_both.stderr)

    def test_auto_layer1_mcp_e2e_guardrail_triggers_only_on_mcp_paths(self):
        # 1. Unrelated Go change must NOT trigger auto Layer 1 MCP E2E.
        (self.logs / "go.log").write_text("")
        sha_other = self.make_commit({"agent_go/cmd/server/main.go": "package main\nfunc main() { _ = 99 }\n"})
        res_other = self.run_verify(sha_other)
        self.assertEqual(res_other.returncode, 0, res_other.stdout + res_other.stderr)
        self.assertNotIn("Auto Layer 1 MCP E2E guardrail triggered", res_other.stdout)
        self.assertNotIn("TestMCPFullChainE2E", (self.logs / "go.log").read_text())

        # 2. MCP/bridge/slotfs change with ZERO Verify-Test lines must auto-run all Layer 1 MCP E2E suites.
        (self.logs / "go.log").write_text("")
        sha_mcp = self.make_commit({
            "mcpagent/agent/coding_agents_bridge.go": "package mcpagent\n",
            "multi-llm-provider-go/internal/clisandbox/landlock.go": "package clisandbox\n",
            "mcp-gateway/e2e/e2e_test.go": "package e2e\n",
            "agent_go/pkg/mcpoauth/store.go": "package mcpoauth\n",
        })
        res_mcp = self.run_verify(sha_mcp)
        self.assertEqual(res_mcp.returncode, 0, res_mcp.stdout + res_mcp.stderr)
        self.assertIn("Auto Layer 1 MCP E2E guardrail triggered", res_mcp.stdout)
        go_log = (self.logs / "go.log").read_text()
        self.assertIn("TestMCPFullChainE2E", go_log)
        self.assertIn("TestMCPBridgeSlotLifecycleE2E", go_log)
        self.assertIn("TestMCPGatewayE2EUpstreamSessionRecovery", go_log)
        self.assertIn("go test ./... -count=1", go_log)

    def test_macos_cli_dependency_gating_parallel_and_isolated_darwin_gocache(self):
        dep_rels = "agent_go/cmd/agentworks agent_go/pkg/agentworksclient workspace/crewroot"

        # 1. Change outside ./cmd/agentworks dependency closure -> macOS CLI skipped.
        (self.logs / "go.log").write_text("")
        sha_browser = self.make_commit({"agent_go/pkg/browser/typing.go": "package browser\n"})
        res_skip = self.run_verify(sha_browser, {"FAKE_CLI_DEP_RELS": dep_rels})
        self.assertEqual(res_skip.returncode, 0, res_skip.stdout + res_skip.stderr)
        self.assertIn("macOS CLI skipped:", res_skip.stdout)
        go_log_skip = (self.logs / "go.log").read_text()
        self.assertNotIn("GOOS=darwin GOARCH=amd64", go_log_skip)
        self.assertNotIn("go build -o /dev/null ./cmd/agentworks", go_log_skip)

        # 2. Change inside an imported package (including nested embed subdir) -> runs arm64 + amd64 in parallel with isolated darwin GOCACHE.
        (self.logs / "go.log").write_text("")
        barrier = self.root / "darwin-barrier"
        barrier.mkdir()
        sha_cli_dep = self.make_commit({"workspace/crewroot/embedded/helper.txt": "hello\n"})
        res_run = self.run_verify(sha_cli_dep, {
            "FAKE_CLI_DEP_RELS": dep_rels,
            "FAKE_DARWIN_BARRIER_DIR": str(barrier),
        })
        self.assertEqual(res_run.returncode, 0, res_run.stdout + res_run.stderr)
        self.assertIn("macOS CLI build and vet (arm64 + amd64 in parallel)", res_run.stdout)
        self.assertTrue((barrier / "arm64.ready").is_file())
        self.assertTrue((barrier / "amd64.ready").is_file())
        go_log_run = (self.logs / "go.log").read_text()
        self.assertIn("GOOS=darwin GOARCH=arm64 GOCACHE=", go_log_run)
        self.assertIn("GOOS=darwin GOARCH=amd64 GOCACHE=", go_log_run)
        self.assertIn("go-build-darwin-1", go_log_run)
        # Linux build stays in go-build-1, never darwin cache.
        self.assertIn("go build ./... (cwd=", go_log_run)
        self.assertIn("go-build-1)", go_log_run)

    def test_mcp_gateway_only_builds_gateway_and_skips_agent_go_build(self):
        (self.logs / "go.log").write_text("")
        sha_gw = self.make_commit({"mcp-gateway/internal/store/vault.go": "package store\n"})
        res_gw = self.run_verify(sha_gw, {"VERIFY_SKIP_AUTO_MCP_E2E": "1"})
        self.assertEqual(res_gw.returncode, 0, res_gw.stdout + res_gw.stderr)
        self.assertIn("go build (mcp-gateway) ./...", res_gw.stdout)
        self.assertNotIn("==> [" + sha_gw[:8] + "] go build ./... (", res_gw.stdout)
        self.assertIn("macOS CLI skipped:", res_gw.stdout)


if __name__ == "__main__":
    unittest.main()
