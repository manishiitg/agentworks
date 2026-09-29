"""Regression checks for the shared rootless Linux deployment pipeline."""
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[1]


class SharedRootlessDeploymentTest(unittest.TestCase):
    def test_products_only_define_configuration_and_assets(self):
        for product in ("confida", "sparkquill", "agents"):
            directory = ROOT / "products" / product
            with self.subTest(product=product):
                self.assertTrue((directory / "product.env").is_file())
                self.assertTrue((directory / "runtime-config.js").is_file())
                # Every deployment builds from the shared catalog.
                self.assertFalse((directory / "mcp-servers.json").exists())
                self.assertFalse(any(directory.glob("*.sh")))

    def test_repository_root_deploy_is_the_only_entry_point(self):
        entry = (REPO / "deploy.sh").read_text()
        self.assertIn("deploy_rootless_product() (", entry)
        self.assertIn('deploy_rootless_product "$SERVER"', entry)
        for removed in ("deploy/rootless-linux/deploy.sh", "deploy/cf/deploy-cf.sh"):
            self.assertFalse((REPO / removed).exists(), removed)

    def test_confida_keeps_required_product_contract(self):
        config = (ROOT / "products/confida/product.env").read_text()
        for expected in (
            "PIN_NODE_VERSION=\"24.21.0\"",
            "CLI_TOOLS=(claude codex pi cursor muse)",
            "COPY_PLAYBOOKS=true",
            "RUN_WORKFLOW_BUILDER_MIGRATION=true",
            "PERSIST_MCP_STATE=true",
            "PUBLIC_HEALTH_STATUS=200",
            'PUBLIC_CHECK_PATHS=("/login")',
            "AGENTWORKS_MCP_STATE_DIR=/srv/confida/state/mcp",
        ):
            self.assertIn(expected, config)

    def test_sparkquill_codex_install_is_pinned_and_launch_checked(self):
        config = (ROOT / "products/sparkquill/product.env").read_text()
        deploy = (REPO / "deploy.sh").read_text()
        self.assertIn("CODEX_CLI_NPM_VERSION=0.156.1", config)
        self.assertIn("--include=optional", deploy)
        self.assertIn('check_lines+="codex --version"', deploy)

    def test_shared_builder_owns_confida_runtime_guards(self):
        deploy = (REPO / "deploy.sh").read_text()
        build = (ROOT / "build-and-activate.sh").read_text()
        for expected in ("install-slack-cli.sh", "agent-browser@latest", "CLI_TOOLS", "to_https_url"):
            self.assertIn(expected, deploy)
        for expected in (
            "RUNTIME_CONFIG_REQUIRED_SNIPPETS",
            "PLAYBOOK_SMOKE_PATH",
            "RUN_WORKFLOW_BUILDER_MIGRATION",
            "RUN_PRODUCT_SECRETS_MIGRATION",
            "PERSIST_MCP_STATE",
            "AGENT_EXTRA_ENV",
            "deployment_checks.py",
            "prune-releases.py",
            '"$BUILD_DIR/downloads/install-agentworks.sh"',
            '"https://$DOMAIN/api/downloads/cli/$file"',
        ):
            self.assertIn(expected, build)


class GogKeyringDeploymentCheckTest(unittest.TestCase):
    """Gmail (gog) must use its file keyring on every headless deployment."""

    def setUp(self):
        import importlib.util
        import tempfile
        spec = importlib.util.spec_from_file_location("deployment_checks", ROOT / "deployment_checks.py")
        self.checks = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.checks)
        self.tmp = Path(tempfile.mkdtemp())

    def env(self, text):
        path = self.tmp / ".env"
        path.write_text(text)
        return path

    def test_env_file_requires_file_backend_and_password(self):
        self.checks.check_gog_keyring_file(self.env("GOG_KEYRING_BACKEND=file\nGOG_KEYRING_PASSWORD=abc\n"))
        for bad in ("", "GOG_KEYRING_BACKEND=file\n", "GOG_KEYRING_BACKEND=auto\nGOG_KEYRING_PASSWORD=abc\n",
                    "GOG_KEYRING_BACKEND=file\nGOG_KEYRING_PASSWORD=\n"):
            with self.subTest(env=bad), self.assertRaises(ValueError):
                self.checks.check_gog_keyring_file(self.env(bad))

    def test_running_process_must_carry_the_keyring_settings(self):
        self.checks.check_gog_keyring_process(b"PATH=/bin\0GOG_KEYRING_BACKEND=file\0GOG_KEYRING_PASSWORD=abc\0")
        for bad in (b"PATH=/bin\0", b"GOG_KEYRING_BACKEND=file\0GOG_KEYRING_PASSWORD=\0"):
            with self.subTest(environ=bad), self.assertRaises(ValueError):
                self.checks.check_gog_keyring_process(bad)

    def test_every_deploy_path_sets_the_keyring_and_updates_gog(self):
        dominion = (REPO / "deploy/dedicated-vm/deploy-dominion.sh").read_text()
        rootless = (ROOT / "build-and-activate.sh").read_text()
        rts = (REPO / "deploy/aws-ec2/server/build-and-activate.sh").read_text()
        entry = (REPO / "deploy.sh").read_text()
        for name, text in (("dominion", dominion), ("rootless", rootless), ("rts", rts)):
            with self.subTest(deploy=name):
                self.assertIn("GOG_KEYRING_BACKEND", text)
                self.assertIn("GOG_KEYRING_PASSWORD", text)
        for name, text in (("dominion", dominion), ("rootless-entry", entry), ("rts", rts)):
            with self.subTest(deploy=name):
                self.assertIn("deploy/common/install-gog.sh", text)
        self.assertIn("deployment_checks.py\" preflight", dominion)
        self.assertIn("deployment_checks.py\" running", dominion)


if __name__ == "__main__":
    unittest.main()
