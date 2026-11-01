"""PLAT-426: choosing an existing build (deploy.sh --build) and the ancestry rule that lets an older build be deployed."""
from pathlib import Path
import json
import os
import shutil
import subprocess
import tempfile
import unittest

DEPLOY = Path(__file__).resolve().parents[1]
REPOS = ("mcp-agent-builder-go", "mcpagent", "multi-llm-provider-go")
FAKE_SSH = """#!/bin/bash
# stands in for ssh to the build host: runs the remote command here, with stdin passed through
while [[ $# -gt 0 ]]; do case "$1" in -p|-o|-i) shift 2 ;; *) break ;; esac; done
shift  # host
exec bash -c "$1"
"""


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True, text=True,
                          env={**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"}).stdout.strip()


class ChosenBuildTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "ssh").write_text(FAKE_SSH)
        (self.bin / "ssh").chmod(0o755)
        self.builds = self.root / "builds"
        self.builds.mkdir()
        self.good, self.off_main = {}, {}
        for repo in REPOS:
            origin = self.root / "origin" / repo
            origin.mkdir(parents=True)
            work = self.root / "seed" / repo
            work.mkdir(parents=True)
            git(work, "init", "-q", "-b", "main")
            (work / "f").write_text("1")
            git(work, "add", "f"); git(work, "commit", "-q", "-m", "one " + repo)
            self.good[repo] = git(work, "rev-parse", "HEAD")
            git(work, "checkout", "-q", "-b", "side")
            (work / "f").write_text("side")
            git(work, "commit", "-qam", "side only " + repo)
            self.off_main[repo] = git(work, "rev-parse", "HEAD")
            git(work, "checkout", "-q", "main")
            (work / "f").write_text("2")
            git(work, "commit", "-qam", "two " + repo)
            git(self.root, "init", "-q", "--bare", str(origin))
            git(work, "push", "-q", str(origin), "main", "side")
            # the local checkouts deploy.sh looks at: this repo and its siblings
            local = self.root / repo
            git(self.root, "clone", "-q", str(origin), str(local))
        shutil.copy(DEPLOY / "common/release_manifest.py", self.root / "mcp-agent-builder-go" / "release_manifest.py")
        (self.root / "mcp-agent-builder-go/deploy/common").mkdir(parents=True)
        shutil.move(self.root / "mcp-agent-builder-go/release_manifest.py", self.root / "mcp-agent-builder-go/deploy/common/release_manifest.py")
        self.add_build("11111111-20261001000000", self.good)
        self.add_build("22222222-20261002000000", {**self.good, "mcpagent": self.off_main["mcpagent"]})

    def add_build(self, name, revs):
        directory = self.builds / name
        directory.mkdir()
        (directory / "manifest.json").write_text(json.dumps({"schema": 1, "name": name, "revisions": revs, "files": {}, "symlinks": {}}))

    def run_deploy_lib(self, script):
        env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}", "BUILDS_DIR": str(self.builds),
               "BUILD_HOST": "example.com", "BUILD_PORT": "22", "BUILD_USER": "builder"}
        return subprocess.run(
            ["bash", "-c", f'set -euo pipefail; REPO_ROOT="{self.root}/mcp-agent-builder-go"; source "{DEPLOY}/common/build-once.sh"; {script}'],
            capture_output=True, text=True, env=env)

    def test_older_build_whose_revisions_are_on_main_is_selected_by_name_or_revision(self):
        for query in ("11111111-20261001000000", "11111111", self.good["mcpagent"][:10]):
            result = self.run_deploy_lib(f'DEPLOY_BUILD="{query}" ensure_prebuilt_build')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "11111111-20261001000000")

    def test_build_with_a_revision_not_on_main_is_refused(self):
        result = self.run_deploy_lib('DEPLOY_BUILD=22222222 ensure_prebuilt_build')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not an ancestor of origin/main", result.stderr)
        self.assertEqual(result.stdout.strip(), "")

    def test_unknown_or_ambiguous_selector_is_refused(self):
        result = self.run_deploy_lib('DEPLOY_BUILD=99999999 ensure_prebuilt_build')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("No unique build matches", result.stderr)
        result = self.run_deploy_lib('DEPLOY_BUILD="bad;rm" ensure_prebuilt_build')
        self.assertNotEqual(result.returncode, 0)

    def test_pinned_name_from_all_hetzner_is_used_without_looking_again(self):
        result = self.run_deploy_lib('DEPLOY_PREBUILT_NAME=33333333-20261003000000 ensure_prebuilt_build')
        self.assertEqual(result.stdout.strip(), "33333333-20261003000000")


    def test_pinned_commits_build_without_a_remote_head_lookup(self):
        for repo in REPOS:
            git(self.root / repo, "remote", "set-url", "origin", "https://github.com/example/" + repo)
        shutil.copy(DEPLOY / "common/build-release.sh", self.root / "mcp-agent-builder-go/deploy/common/build-release.sh")
        pins = "; ".join(f'DEPLOY_SHA_{repo.upper().replace("-", "_")}={self.good[repo]}' for repo in REPOS)
        result = self.run_deploy_lib("""
            git() {
                if [[ "$1" == ls-remote ]]; then echo "unexpected network lookup" >&2; return 97; fi
                command git "$@"
            }
            build_ssh() {
                if [[ "$1" == *systemd-run* ]]; then echo BUILD_NAME=pinned-build; else cat >/dev/null; fi
            }
        """ + pins + "; build_release_remote")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "pinned-build")


class EntryPointTest(unittest.TestCase):
    def test_deploy_sh_parses_and_exposes_the_new_commands(self):
        subprocess.run(["bash", "-n", str(DEPLOY.parent / "deploy.sh")], check=True)
        help_text = subprocess.run([str(DEPLOY.parent / "deploy.sh"), "--help"], capture_output=True, text=True).stdout
        for word in ("all-hetzner", "builds", "build [--force]", "--build <name|sha>", "pin|unpin"):
            self.assertIn(word, help_text)

    def test_all_hetzner_excludes_separated_products(self):
        entry = (DEPLOY.parent / "deploy.sh").read_text()
        block = entry[entry.index("\n  all-hetzner)\n"):entry.index("\n  -h|--help|help)")]
        # PLAT-856-454: platform batches exclude separately developed products.
        self.assertNotIn("dominion", block.lower().replace("never dominion", ""))
        self.assertNotIn("sparkquill", block.lower())
        self.assertEqual([l.strip() for l in block.splitlines() if l.strip().startswith("deploy_rootless_product ")],
                         ["deploy_rootless_product agents", "deploy_rootless_product confida"])



if __name__ == "__main__":
    unittest.main()
