"""PLAT-426: the rootless activation copies a verified prebuilt release instead of compiling, and refuses a bad one.

Runs build-and-activate.sh --prebuilt --stage-only against a fake build in a scratch folder (Linux only: it needs ldd,
sha256sum, file, openssl, node). Nothing is activated: --stage-only stops before preflight, `current` and every service.
"""
from pathlib import Path
import importlib.util
import json
import os
import platform
import shutil
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[1]
spec = importlib.util.spec_from_file_location("release_manifest", REPO / "deploy/common/release_manifest.py")
release_manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release_manifest)

TOOLS = ("bash", "python3", "file", "sha256sum", "openssl", "ldd", "node")
REVS = {"mcp-agent-builder-go": "a1" * 20, "mcpagent": "b2" * 20, "multi-llm-provider-go": "c3" * 20}


@unittest.skipUnless(platform.system() == "Linux" and all(shutil.which(t) for t in TOOLS), "needs Linux with " + ", ".join(TOOLS))
class PrebuiltActivationTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = Path(self._tmp.name)
        self.build = self.tmp / "aaaaaaaa-20261004000000"
        self.app = self.tmp / "app"
        source = self.build / "source"
        builder = source / "mcp-agent-builder-go"
        shutil.copytree(REPO / "deploy", builder / "deploy", ignore=shutil.ignore_patterns("__pycache__", "*.test"))
        (builder / "frontend/scripts").mkdir(parents=True)
        shutil.copy(REPO / "frontend/scripts/check-release-assets.mjs", builder / "frontend/scripts/")
        (builder / "agent_go/configs").mkdir(parents=True)
        shutil.copy(REPO / "agent_go/configs/mcp_servers_clean.json", builder / "agent_go/configs/")
        for other in ("mcpagent", "multi-llm-provider-go"):
            (source / other).mkdir()
            (source / other / "go.mod").write_text("module x\n")
        (self.build / "bin/lib").mkdir(parents=True)
        for name in ("agent", "workspace", "gateway", "browser", "slotctl", "slottmux", "mcpbridge", "video-studio-landlock-runner"):
            shutil.copy("/bin/true", self.build / "bin" / name)  # a real dynamic executable, so the ldd check has something to load
        shutil.copy("/bin/true", self.build / "bin/agentworks-vault")
        shutil.copy(ROOT.parent / 'common/install-vault-service.py', self.build / 'bin/install-vault-service.py')
        (self.build / 'bin/install-vault-service.py').chmod(0o755)
        (self.build / "bin/lib/libfake.so").write_text("lib")
        (self.build / "frontend/assets").mkdir(parents=True)
        (self.build / "frontend/index.html").write_text('<html><head><title>AgentWorks</title><link rel="icon" href="/x.ico" /></head></html>')
        (self.build / "frontend/report-preview.js").write_text("preview")
        old_asset = self.build / "frontend/assets/app.js"
        old_asset.write_text("x=1")
        forty_days = time.time() - 40 * 86400
        os.utime(old_asset, (forty_days, forty_days))
        (self.build / "static").mkdir()
        (self.build / "static/report-preview.js").write_text("preview")
        (self.build / "downloads").mkdir()
        (self.build / "downloads/agentworks-linux-amd64").write_text("cli")
        (self.build / "downloads/install-agentworks.sh").write_text("#!/bin/sh\n")
        (self.build / "SOURCE_REVISIONS").write_text("".join(f"{k}={v}\n" for k, v in REVS.items()))
        release_manifest.create(self.build, self.build.name, REVS)

    def activate(self, *extra, product="sparkquill", env=None, workspace=None):
        full_env = {**os.environ, "DEPLOY_APP_ROOT": str(self.app), "HOME": str(self.tmp / "home"), **(env or {})}
        return subprocess.run(
            ["bash", str(self.build / "source/mcp-agent-builder-go/deploy/rootless-linux/build-and-activate.sh"),
             str(workspace or self.build / "source"), product, "--prebuilt", str(self.build), *extra],
            capture_output=True, text=True, env=full_env)

    def releases(self):
        folder = self.app / "releases"
        return sorted(folder.iterdir()) if folder.is_dir() else []

    def test_stage_only_copies_the_build_into_the_products_own_release(self):
        result = self.activate("--stage-only")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("no compile", result.stdout)
        self.assertIn("Stage only", result.stdout)
        (release,) = self.releases()
        self.assertTrue(release.name.startswith("sparkquill-a1a1a1a1-"))
        for name in ("sparkquill-agent", "sparkquill-workspace", "sparkquill-gateway", "slotctl", "slottmux", "mcpbridge",
                     "video-studio-landlock-runner", "lib/libfake.so"):
            self.assertTrue((release / "bin" / name).exists(), name)
        for name in ("agent", "workspace", "gateway", "browser"):
            self.assertFalse((release / "bin" / name).exists(), name)  # renamed per product, or RTS-only
        self.assertEqual(json.loads((release / "downloads/version.json").read_text())["version"], REVS["mcp-agent-builder-go"])
        self.assertTrue((release / "downloads/agentworks-linux-amd64").is_file())
        self.assertEqual((release / "SOURCE_REVISIONS").read_text(), (self.build / "SOURCE_REVISIONS").read_text())
        self.assertTrue((release / "source/mcpagent/go.mod").is_file())
        self.assertTrue((release / "static/report-preview.js").is_file())
        self.assertTrue((release / "configs/mcp_servers_sparkquill.json").is_file())
        self.assertEqual((release / "frontend/runtime-config.js").read_text(),
                         (ROOT / "products/example/runtime-config.js").read_text())
        self.assertTrue((release / "check-release-assets.mjs").is_file())
        self.assertTrue((release / "deployment_checks.py").is_file())
        # nothing was activated
        self.assertFalse((self.app / "current").exists())
        self.assertFalse((release / ".deploying").exists())
        # copied files are new files: the 14-day cleanup of carried assets must not delete an old build's assets
        self.assertGreater((release / "frontend/assets/app.js").stat().st_mtime, time.time() - 3600)

    def test_playbook_validators_never_write_into_the_shared_build_source(self):
        # Confida ships playbooks. Its first prebuilt deploy failed because the playbook tests create a temporary folder beside the playbooks, in the
        # build's read-only source (2026-10-04). A product validates its own copy; the shared source must not be touched.
        playbooks = self.build / "source/mcp-agent-builder-go/playbooks"
        (playbooks / "scripts").mkdir(parents=True)
        (playbooks / "scripts/validate_playbooks.py").write_text(
            "import pathlib, tempfile, sys\n"
            "here = pathlib.Path(__file__).resolve().parent.parent\n"
            "tempfile.mkdtemp(dir=here, prefix='graph-test-')\n"  # fails with PermissionError in a read-only tree
        )
        smoke = playbooks / "agentic-engineering-platform/browser-qa/basic-browser-setup/playbook.json"
        smoke.parent.mkdir(parents=True)
        smoke.write_text("{}")
        release_manifest.create(self.build, self.build.name, REVS)  # the build now includes these files
        for path in [playbooks, *playbooks.rglob("*")]:
            if path.is_dir():
                path.chmod(0o555)
        self.addCleanup(lambda: [p.chmod(0o755) for p in [playbooks, *playbooks.rglob("*")] if p.is_dir()])
        # as root the permission bits do not apply, so only prove the shared source stays unchanged
        before = sorted(str(p) for p in playbooks.rglob("*"))
        result = self.activate("--stage-only", product="confida")
        self.assertEqual(sorted(str(p) for p in playbooks.rglob("*")), before, "the shared build source was written to")
        if os.geteuid() != 0:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            (release,) = self.releases()
            self.assertTrue((release / "playbooks/scripts/validate_playbooks.py").is_file())

    def refuse(self, fragment, *extra, **kwargs):
        result = self.activate("--stage-only", *extra, **kwargs)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(fragment, result.stderr)
        self.assertEqual(self.releases(), [], "a refused build must leave no release behind")

    def test_hash_mismatch_is_refused_before_anything_is_written(self):
        (self.build / "frontend/assets/app.js").write_text("x=2")
        self.refuse("hash mismatch for frontend/assets/app.js")

    def test_missing_file_is_refused(self):
        (self.build / "bin/lib/libfake.so").unlink()
        self.refuse("missing file bin/lib/libfake.so")

    def test_wrong_architecture_is_refused(self):
        data = json.loads((self.build / "manifest.json").read_text())
        data["arch"] = "aarch64" if platform.machine() != "aarch64" else "x86_64"
        (self.build / "manifest.json").write_text(json.dumps(data))
        self.refuse("this host is " + platform.machine())

    def test_newer_glibc_than_the_host_is_refused(self):
        data = json.loads((self.build / "manifest.json").read_text())
        data["glibc"] = "99.1"
        (self.build / "manifest.json").write_text(json.dumps(data))
        self.refuse("needs glibc 99.1 or newer")

    def test_workspace_root_must_be_the_builds_source(self):
        result = self.activate("--stage-only", workspace=self.tmp)
        self.assertEqual(result.returncode, 2)
        self.assertIn("WORKSPACE_ROOT must be", result.stderr)

    def test_scratch_app_root_is_only_for_stage_only(self):
        result = self.activate()  # no --stage-only, DEPLOY_APP_ROOT set
        self.assertEqual(result.returncode, 2)
        self.assertIn("only allowed together with --stage-only", result.stderr)
        self.assertEqual(self.releases(), [])


class RtsShipmentTest(unittest.TestCase):
    """The RTS tarball is a trimmed copy of the build; its activation names binaries the RTS way and verifies with explicit skips."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = Path(self._tmp.name)
        self.build = self.tmp / "aaaaaaaa-20261004000000"
        for rel in ("bin/agent", "bin/workspace", "bin/gateway", "bin/browser", "bin/slotctl", "bin/workspace-security.test",
                    "bin/update-coding-clis", "bin/lib/libfake.so", "source/mcp-agent-builder-go/a.go", "source/mcpagent/b.go",
                    "source/multi-llm-provider-go/c.go", "downloads/agentworks-linux-amd64", "frontend/index.html"):
            path = self.build / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(rel)
        release_manifest.create(self.build, self.build.name, REVS)
        data = json.loads((self.build / "manifest.json").read_text())
        data.update(os="linux", arch="x86_64", glibc="2.39")  # deterministic whatever machine runs this test
        (self.build / "manifest.json").write_text(json.dumps(data))
        self.shipped = self.tmp / "rts"
        self.shipped.mkdir()
        # exactly the tar command of ship_build_to_rts in deploy/common/build-once.sh
        name = self.build.name
        subprocess.run(
            f"tar -C '{self.tmp}' --exclude='{name}/source/mcpagent' --exclude='{name}/source/multi-llm-provider-go' "
            f"--exclude='{name}/downloads' -cf - '{name}' | tar -C '{self.shipped}' -xf -", shell=True, check=True)
        self.copy = self.shipped / name

    def verify(self, *skips):
        return release_manifest.verify(self.copy, skip_prefixes=skips, arch="x86_64", glibc="2.39")

    def test_trimmed_copy_verifies_only_with_the_documented_skips(self):
        with self.assertRaises(release_manifest.Refused):
            self.verify()
        self.verify("source/mcpagent/", "source/multi-llm-provider-go/", "downloads/")

    def test_a_skip_does_not_hide_a_damaged_file(self):
        (self.copy / "bin/slotctl").write_text("bin/slotXXX")
        with self.assertRaises(release_manifest.Refused) as caught:
            self.verify("source/mcpagent/", "source/multi-llm-provider-go/", "downloads/")
        self.assertIn("bin/slotctl", str(caught.exception))

    @unittest.skipUnless(shutil.which("bash"), "bash")
    def test_binaries_are_named_per_target(self):
        for prefix, rename, skip, expected, absent in (
            ("video-studio", "agent workspace gateway browser", "update-coding-clis",
             {"video-studio-agent", "video-studio-workspace", "video-studio-gateway", "video-studio-browser", "slotctl", "workspace-security.test", "lib"},
             {"agent", "update-coding-clis"}),
            ("sparkquill", "agent workspace gateway", "browser workspace-security.test update-coding-clis",
             {"sparkquill-agent", "sparkquill-workspace", "sparkquill-gateway", "slotctl", "lib"},
             {"agent", "browser", "workspace-security.test", "update-coding-clis"}),
        ):
            with self.subTest(prefix=prefix):
                dest = self.tmp / f"dest-{prefix}"
                subprocess.run(["bash", "-c", f'source "{REPO}/deploy/common/prebuilt.sh"; prebuilt_copy_bin "{self.copy}" "{dest}" {prefix} "{rename}" "{skip}"'], check=True)
                names = {p.name for p in dest.iterdir()}
                self.assertTrue(expected <= names, names)
                self.assertFalse(absent & names, names)


if __name__ == "__main__":
    unittest.main()
