"""PLAT-426: how deploy.sh gets a build onto RTS: download from GitHub, the stream fallback, forced modes, and `builds`/`publish`."""
from pathlib import Path
import hashlib
import os
import subprocess
import tempfile
import unittest

from fake_github import FakeGithub, TOKEN
from test_build_once import FAKE_SSH
from test_publish_build import TAG, make_build

COMMON = Path(__file__).resolve().parent
DEPLOY_SH = COMMON.parents[1] / "deploy.sh"


class TransportTest(unittest.TestCase):
    def setUp(self):
        self.gh = FakeGithub()
        self.addCleanup(self.gh.close)
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "ssh").write_text(FAKE_SSH)
        (self.bin / "ssh").chmod(0o755)
        self.builds = self.root / "builds"
        self.builds.mkdir()
        self.build = make_build(self.builds)
        self.sha = hashlib.sha256((self.build / "manifest.json").read_bytes()).hexdigest()
        self.home = self.root / "home"
        self.home.mkdir()
        self.job = self.root / "rts/job"
        self.job.mkdir(parents=True)
        self.marker = self.root / "streamed"

    def token(self):
        d = self.home / ".config/agentworks"
        d.mkdir(parents=True)
        (d / "builds.env").write_text(f"GH_TOKEN={TOKEN}\n")
        (d / "builds.env").chmod(0o600)

    def run_lib(self, script, **env):
        base = {k: v for k, v in os.environ.items() if k not in ("GH_TOKEN", "DEPLOY_BUILD_TRANSPORT")}
        base.update(self.gh.env(), PATH=f"{self.bin}:{os.environ['PATH']}", BUILDS_DIR=str(self.builds), HOME=str(self.home), **env)
        code = (f'set -euo pipefail; REPO_ROOT="{COMMON.parents[1]}"; source "{COMMON}/build-once.sh"; SSH=(ssh rts-host); '
                f'ship_build_to_rts() {{ echo "$1 $2" > "{self.marker}"; mkdir -p "$2/build"; }}; {script}')
        return subprocess.run(["bash", "-c", code], capture_output=True, text=True, env=base)

    def deliver(self, sha=None, **env):
        return self.run_lib(f'deliver_build_to_rts "{self.build.name}" "{self.job}" "{sha or self.sha}"', **env)

    def test_auto_with_a_token_publishes_and_rts_downloads_it_itself(self):
        self.token()
        r = self.deliver()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"RTS downloads build {self.build.name} itself from GitHub ({TAG})", r.stderr)
        self.assertFalse(self.marker.exists(), "the stream path must not run")
        self.assertEqual((self.job / "build/manifest.json").read_bytes(), (self.build / "manifest.json").read_bytes())
        self.assertEqual([d[1] for d in self.gh.downloads], ["build-rts.tar.gz"])
        self.assertFalse((self.job / "build/downloads").exists())

    def test_auto_without_a_token_falls_back_to_the_stream_with_one_reason(self):
        r = self.deliver()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("Falling back to streaming the build through this machine: the build is not on GitHub", r.stderr)
        self.assertTrue(self.marker.exists())
        self.assertEqual(self.gh.downloads, [])

    def test_auto_falls_back_when_the_download_fails_verification(self):
        self.token()
        r = self.deliver(sha="0" * 64)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("RTS could not download and verify", r.stderr)
        self.assertTrue(self.marker.exists())
        self.assertFalse((self.job / "build").exists() and (self.job / "build/manifest.json").exists())

    def test_github_mode_never_falls_back(self):
        r = self.deliver(DEPLOY_BUILD_TRANSPORT="github")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("DEPLOY_BUILD_TRANSPORT=github", r.stderr)
        self.assertFalse(self.marker.exists())
        self.token()
        r = self.deliver(sha="0" * 64, DEPLOY_BUILD_TRANSPORT="github")
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(self.marker.exists())

    def test_stream_mode_does_not_touch_github(self):
        self.token()
        r = self.deliver(DEPLOY_BUILD_TRANSPORT="stream")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(self.marker.exists())
        self.assertEqual(self.gh.log, [])

    def test_unknown_transport_is_refused(self):
        r = self.deliver(DEPLOY_BUILD_TRANSPORT="carrier-pigeon")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("must be auto, github or stream", r.stderr)

    def deploy(self, *args, **env):
        base = {k: v for k, v in os.environ.items() if k != "GH_TOKEN"}
        base.update(self.gh.env(), PATH=f"{self.bin}:{os.environ['PATH']}", BUILDS_DIR=str(self.builds), HOME=str(self.home), **env)
        return subprocess.run([str(DEPLOY_SH), *args], capture_output=True, text=True, env=base)

    def test_publish_command_and_builds_listing(self):
        self.token()
        r = self.deploy("publish")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"Published: {self.build.name} -> {TAG}", r.stdout)
        r = self.deploy("builds")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("On GitHub", r.stdout)
        self.assertIn(f"{TAG}", r.stdout)
        self.assertIn("complete", r.stdout)

    def test_publish_command_without_a_token_says_so_and_fails_only_that_command(self):
        r = self.deploy("publish", self.build.name[:8])
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("publish skipped: no GitHub token", r.stderr + r.stdout)

    def test_deploy_sh_uses_the_transport_and_documents_it(self):
        text = DEPLOY_SH.read_text()
        self.assertIn('deliver_build_to_rts "$prebuilt_name" "$REMOTE_JOB"', text)
        self.assertNotIn('\n    ship_build_to_rts "$prebuilt_name"', text)
        help_text = subprocess.run([str(DEPLOY_SH), "--help"], capture_output=True, text=True).stdout
        for word in ("DEPLOY_BUILD_TRANSPORT=auto|github|stream", "publish [build]"):
            self.assertIn(word, help_text)
        # Hetzner products still copy from /srv/_builds
        self.assertIn('printf \'%s\\n\' "$BUILDS_DIR/$PREBUILT_NAME" > "$STAGING/prebuilt"', text)


if __name__ == "__main__":
    unittest.main()
