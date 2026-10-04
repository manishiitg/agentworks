"""PLAT-478: the deploy makes the Landlock launcher reachable for slot accounts, and every slot host runs the read-only
slot self-test (deploy/common/slotcheck.sh) at the end of the deploy and fails it loudly; the secret admission scan
reports names only.
"""
import hashlib
import io
import json
import os
import stat
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
SLOTS_SH = HERE / "slots.sh"
SLOTCHECK_SH = HERE / "slotcheck.sh"

import importlib.util

_spec = importlib.util.spec_from_file_location("admission_scan", HERE / "admission_scan.py")
scan_mod = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(scan_mod)


def mode(path):
    return stat.S_IMODE(os.stat(path).st_mode)


class ReleaseTraversalTest(unittest.TestCase):
    def run_traversal(self, app, build, slots_enabled="true"):
        subprocess.run(
            ["bash", "-c", f'set -euo pipefail; SLOTS_ENABLED={slots_enabled}; source "{SLOTS_SH}"; '
                           f'slots_release_traversal "{app}" "{build}"'],
            check=True,
        )

    def layout(self, tmp):
        app = Path(tmp) / "app"
        build = app / "releases" / "r1"
        (build / "bin").mkdir(parents=True)
        runner = build / "bin" / "video-studio-landlock-runner"
        runner.write_text("x")
        # The RTS layout of 2026-10-04: releases/ 0700 (a umask-077 mkdir), so slots could not reach the launcher.
        os.chmod(app / "releases", 0o700)
        os.chmod(build, 0o700)
        os.chmod(build / "bin", 0o700)
        os.chmod(runner, 0o700)
        return app, build, runner

    def test_rts_layout_becomes_traversable_but_not_listable(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, build, runner = self.layout(tmp)
            self.run_traversal(app, build)
            self.assertEqual(mode(app / "releases"), 0o711)
            self.assertTrue(mode(build) & 0o001, "the release folder must be searchable by others")
            self.assertFalse(mode(build) & 0o004, "the release folder must not become world-listable")
            self.assertTrue(mode(build / "bin") & 0o001)
            self.assertFalse(mode(build / "bin") & 0o004)
            self.assertEqual(mode(runner), 0o755)

    def test_idempotent(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, build, runner = self.layout(tmp)
            self.run_traversal(app, build)
            first = [mode(p) for p in (app / "releases", build, build / "bin", runner)]
            self.run_traversal(app, build)
            self.assertEqual(first, [mode(p) for p in (app / "releases", build, build / "bin", runner)])

    def test_hetzner_775_releases_is_tightened_to_0711(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, build, _ = self.layout(tmp)
            os.chmod(app / "releases", 0o775)
            self.run_traversal(app, build)
            self.assertEqual(mode(app / "releases"), 0o711)

    def test_host_without_slots_is_untouched(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, build, runner = self.layout(tmp)
            self.run_traversal(app, build, slots_enabled="false")
            if not os.path.exists("/etc/agentworks/slots.json"):
                self.assertEqual(mode(app / "releases"), 0o700)
                self.assertEqual(mode(runner), 0o700)

    def test_slotcheck_is_built_into_every_release(self):
        text = SLOTS_SH.read_text()
        self.assertIn("for program in slotctl slottmux slotcheck; do", text)


class WiringTest(unittest.TestCase):
    def test_both_activation_scripts_set_traversal_and_run_the_self_test(self):
        for script in ("deploy/rootless-linux/build-and-activate.sh", "deploy/aws-ec2/server/build-and-activate.sh"):
            text = (REPO / script).read_text()
            with self.subTest(script=script):
                self.assertIn('slots_release_traversal "$REMOTE_APP" "$BUILD_DIR"', text)
                self.assertIn("slots_selfcheck ", text)
                self.assertIn('cp "$REPO_ROOT/deploy/common/slotcheck.sh" "$BUILD_DIR/slotcheck.sh"', text)
                self.assertIn('cp "$REPO_ROOT/deploy/common/admission_scan.py" "$BUILD_DIR/admission_scan.py"', text)
                # The self-test fails the deploy (after activation, so nothing is rolled back) ...
                selfcheck = text.index("slots_selfcheck ")
                self.assertIn("exit 1", text[selfcheck:selfcheck + 400])
                # ... and runs after the release is switched to current.
                self.assertGreater(selfcheck, text.index('ln -sfn'))
                # Traversal happens before the release is activated.
                self.assertLess(text.index("slots_release_traversal"), text.index("vault_install"))

    def test_deploy_sh_has_a_standalone_slotcheck(self):
        text = (REPO / "deploy.sh").read_text()
        self.assertIn('if [[ "$SERVER" == slotcheck ]]; then', text)
        self.assertIn("current/slotcheck.sh --app /var/lib/video-studio/video-studio --docs /data/video-studio/docs --product video-studio", text)
        self.assertIn('current/slotcheck.sh --app /srv/$product --docs /srv/$product/data/docs --product $product', text)
        subprocess.run(["bash", "-n", str(REPO / "deploy.sh")], check=True)

    def test_slotcheck_sh_is_read_only(self):
        text = "\n".join(line for line in SLOTCHECK_SH.read_text().splitlines() if not line.lstrip().startswith("#"))
        for forbidden in ("chmod", "chown", "chgrp", "setfacl", "rm -", "mv ", "sudo ", "> /etc", "install "):
            self.assertNotIn(forbidden, text, f"the self-test wrapper must not change anything ({forbidden})")


class SlotcheckWrapperTest(unittest.TestCase):
    """slotcheck.sh with a fake release: it passes the service's slot settings (only those) and the exit code."""

    def make_release(self, tmp, exit_code, slots_mode="optin"):
        app = Path(tmp) / "app"
        release = app / "releases" / "r1"
        (release / "bin").mkdir(parents=True)
        captured = Path(tmp) / "captured"
        (release / "bin" / "slotcheck").write_text(
            "#!/bin/sh\nenv > " + str(captured) + "\necho 'PASS  pwd-crew-project  slot01  <docs>/...'\nexit " + str(exit_code) + "\n")
        os.chmod(release / "bin" / "slotcheck", 0o755)
        (release / "admission_scan.py").write_text((HERE / "admission_scan.py").read_text())
        (app / "current").symlink_to(release)
        (app / ".env").write_text(f"AGENTWORKS_SLOTS={slots_mode}\nAGENT_BROWSER_SHARED_PROFILE=/srv/x/state/browser-profile\n"
                                  "CAPLAYER_SERVICE_TOKEN=do-not-pass-this-token-anywhere-0123456789\nGLOBAL_SECRET_X=value-must-not-leak\n")
        docs = Path(tmp) / "docs"
        docs.mkdir()
        return app, docs, captured

    def run_wrapper(self, app, docs):
        bin_dir = Path(app).parent / "fakebin"
        bin_dir.mkdir(exist_ok=True)
        (bin_dir / "systemctl").write_text("#!/bin/sh\necho 0\n")
        os.chmod(bin_dir / "systemctl", 0o755)
        env = dict(os.environ, PATH=f"{bin_dir}:{os.environ['PATH']}")
        return subprocess.run(["bash", str(SLOTCHECK_SH), "--app", str(app), "--docs", str(docs), "--product", "agents"],
                              capture_output=True, text=True, env=env)

    def test_failure_is_propagated_and_loud(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, docs, captured = self.make_release(tmp, 1)
            result = self.run_wrapper(app, docs)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn("SLOT SELF-TEST FAILED", result.stderr)
            passed = captured.read_text()
            self.assertIn("AGENTWORKS_SLOTS=optin", passed)
            self.assertIn("AGENT_BROWSER_SHARED_PROFILE=/srv/x/state/browser-profile", passed)
            self.assertNotIn("CAPLAYER_SERVICE_TOKEN", passed, "only the slot keys reach the self-test")
            self.assertNotIn("GLOBAL_SECRET_X", passed)
            self.assertNotIn("value-must-not-leak", result.stdout + result.stderr)
            self.assertNotIn("do-not-pass-this-token", result.stdout + result.stderr)

    def test_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, docs, _ = self.make_release(tmp, 0)
            result = self.run_wrapper(app, docs)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("secret admission scan", result.stdout)

    def test_host_without_slots_skips(self):
        with tempfile.TemporaryDirectory() as tmp:
            app, docs, captured = self.make_release(tmp, 1, slots_mode="")
            result = self.run_wrapper(app, docs)
            self.assertEqual(result.returncode, 0)
            self.assertFalse(captured.exists())


class SecretAdmissionScanTest(unittest.TestCase):
    def build(self, tmp):
        docs = Path(tmp)
        (docs / "_users" / "_system_global_secrets").mkdir(parents=True)
        (docs / "_users" / "_system_global_secrets" / "secrets.json").write_text(json.dumps({"NOTION_KEY": {"value": "enc:aaa"}, "UNGRANTED": {"value": "enc:bbb"}}))
        wf = docs / "Workflow" / "daily"
        wf.mkdir(parents=True)
        (wf / "workflow.json").write_text(json.dumps({"capabilities": {
            "selected_global_secret_names": ["NOTION_KEY", "UNGRANTED", "ENV_ONE", "GONE"],
            "selected_secrets": ["LOCAL_OK", "LOCAL_GONE"]}}))
        digest = hashlib.sha256(b"Workflow/daily").hexdigest()
        (docs / "_users" / "_shared" / "workflow_secrets").mkdir(parents=True)
        (docs / "_users" / "_shared" / "workflow_secrets" / f"{digest}.json").write_text(json.dumps({"workflow_path": "Workflow/daily", "secrets": {"LOCAL_OK": "enc:ccc"}}))
        crew = docs / "_users" / "u-123" / "Chats" / "Work" / "projects" / "c1"
        crew.mkdir(parents=True)
        (crew / "product.json").write_text(json.dumps({"capabilities": {"selected_global_secret_names": ["NOTION_KEY"]}}))
        return docs

    def test_reports_names_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            docs = self.build(tmp)
            env = {"GLOBAL_SECRET_ENV_ONE": "plain-value-xyz"}
            warnings, counts = scan_mod.scan(docs, env, grants={"NOTION_KEY", "ENV_ONE"})
            text = "\n".join(warnings)
            self.assertIn("secret-missing Workflow/daily/workflow.json: selected_global_secret_names names GONE", text)
            self.assertIn("names LOCAL_GONE", text)
            self.assertNotIn("LOCAL_OK", text)
            self.assertIn("secret-no-platform-grant UNGRANTED: selected by 1 manifest(s)", text)
            self.assertNotIn("secret-no-platform-grant NOTION_KEY", text)
            for value in ("enc:aaa", "enc:bbb", "enc:ccc", "plain-value-xyz"):
                self.assertNotIn(value, text)
            self.assertEqual(counts["manifests"], 2)

    def test_user_id_is_not_printed(self):
        with tempfile.TemporaryDirectory() as tmp:
            docs = self.build(tmp)
            crew = docs / "_users" / "u-123" / "Chats" / "Work" / "projects" / "c1" / "product.json"
            crew.write_text(json.dumps({"capabilities": {"selected_secrets": ["NOPE"]}}))
            warnings, _ = scan_mod.scan(docs, {}, grants=None)
            text = "\n".join(warnings)
            self.assertIn("_users/<user>/Chats/Work/projects/c1/product.json", text)
            self.assertNotIn("u-123", text)

    def test_without_vault_grants_are_not_checked(self):
        grants, note = scan_mod.platform_grants({})
        self.assertIsNone(grants)
        self.assertIn("not configured", note)

    def test_platform_group_id_matches_the_gateway(self):
        # mcp-gateway internal/store/platform_group.go: "everyone-" + hex(sha256("w1")[:12])
        self.assertEqual(scan_mod.platform_group_id("w1"), "everyone-" + hashlib.sha256(b"w1").hexdigest()[:24])

    def test_main_never_prints_the_token(self):
        with tempfile.TemporaryDirectory() as tmp:
            docs = self.build(tmp)
            env_file = Path(tmp) / ".env"
            env_file.write_text("CAPLAYER_SERVICE_URL=http://127.0.0.1:1\nCAPLAYER_SERVICE_TOKEN=tok-0123456789abcdef0123456789abcdef\n")
            out = io.StringIO()
            with redirect_stdout(out):
                self.assertEqual(scan_mod.main(["--docs", str(docs), "--env-file", str(env_file)]), 0)
            self.assertNotIn("tok-0123456789abcdef", out.getvalue())
            self.assertIn("grants not checked", out.getvalue())


if __name__ == "__main__":
    unittest.main()
