"""Exercise the shared renderer's persistent credentials and least authority."""
import importlib.util
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('vault_install', ROOT / 'install-vault-service.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class VaultInstallTest(unittest.TestCase):
    def test_prebuilt_copy_preserves_vault_assets_and_needs_no_compiler(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            build, release = root / 'build', root / 'release'
            (build / 'bin').mkdir(parents=True)
            for name in ('agent', 'workspace', 'gateway', 'agentworks-vault', 'install-vault-service.py'):
                asset = build / 'bin' / name
                asset.write_text('#!/bin/sh\nexit 0\n')
                asset.chmod(0o755)
            script = '\n'.join([
                'set -euo pipefail', '. "$1/prebuilt.sh"', '. "$1/vault.sh"',
                'VAULT_ENABLED=true',
                'go() { echo "activation tried to compile" >&2; return 99; }',
                'prebuilt_copy_bin "$2" "$3/bin" agents "agent workspace gateway" ""',
                'vault_check_build "$3"',
            ])
            command = ['bash', '-c', script, 'test', str(ROOT), str(build), str(release)]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((release / 'bin/agentworks-vault').read_bytes(), (build / 'bin/agentworks-vault').read_bytes())
            self.assertTrue(os.access(release / 'bin/install-vault-service.py', os.X_OK))
            (build / 'bin/install-vault-service.py').unlink()
            (release / 'bin/install-vault-service.py').unlink()
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('lacks its binary or installer', result.stderr)

    def test_repeat_preserves_credential_and_private_storage(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app, docs, units = root / 'app', root / 'docs', root / 'units'
            app.mkdir()
            (app / '.env').write_text('VAULT_AUDIT_PROVIDER=sqlite\n')
            state = module.render(app, docs, 'agents', 24000, 24003, units)
            first = (state / 'service-token').read_bytes()
            module.render(app, docs, 'agents', 24000, 24003, units)
            self.assertEqual(first, (state / 'service-token').read_bytes())
            for name in ('service-token', 'service.env', 'agent.env'):
                self.assertEqual((state / name).stat().st_mode & 0o777, 0o600)
            env = module.read_env(state / 'service.env')
            self.assertEqual(env['GATEWAY_AUTH_MODE'], 'platform')
            self.assertEqual(env['GATEWAY_BIND'], '127.0.0.1')
            self.assertEqual(env['GATEWAY_WORKSPACE_DIR'], str(docs / 'Chats/CapLayer'))
            self.assertEqual(env['GATEWAY_UPSTREAM_URL'], 'none')
            self.assertEqual(env['LOCAL_MODE'], 'false')
            self.assertNotIn('GATEWAY_GRANT_TOOLS', env)
            self.assertIn('UMask=0077', (units / 'agents-vault.service').read_text())
            self.assertIn('EnvironmentFile=', (units / 'agents-agent.service.d/zz-vault.conf').read_text())

    def test_sqlite_is_default_and_other_storage_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app = root / 'app'
            app.mkdir()
            state = module.render(app, root / 'docs', 'agents', 24000, 24003, root / 'units')
            self.assertEqual(module.read_env(state / 'service.env')['VAULT_AUDIT_PROVIDER'], 'sqlite')
            before = (state / 'service.env').read_bytes()
            (app / '.env').write_text('VAULT_AUDIT_PROVIDER=clickhouse\n')
            with self.assertRaisesRegex(ValueError, 'must be sqlite or off'):
                module.render(app, root / 'docs', 'agents', 24000, 24003, root / 'units')
            self.assertEqual((state / 'service.env').read_bytes(), before)
            (app / '.env').write_text('VAULT_AUDIT_PROVIDER=off\nVAULT_AUDIT_RETENTION=12h\n')
            state = module.render(app, root / 'docs', 'agents', 24000, 24003, root / 'units')
            env = module.read_env(state / 'service.env')
            self.assertEqual(env['VAULT_AUDIT_PROVIDER'], 'off')
            self.assertEqual(env['VAULT_AUDIT_RETENTION'], '12h')

    def test_refuses_workspace_state_and_symlink_token(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app = root / 'app'
            app.mkdir()
            (app / '.env').write_text('VAULT_AUDIT_PROVIDER=sqlite\n')
            with self.assertRaises(ValueError):
                module.render(app, root, 'agents', 24000, 24003, root / 'units')
            state = app / 'state/vault'
            state.mkdir(parents=True)
            (state / 'service-token').symlink_to(root / 'other')
            with self.assertRaisesRegex(ValueError, 'symlink'):
                module.render(app, root / 'docs', 'agents', 24000, 24003, root / 'units')

    def test_failed_bootstrap_recovers_service_and_keeps_error(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'state/vault').mkdir(parents=True)
            (root / 'state/vault/service.env').write_text('GATEWAY_AUTH_MODE=platform\n')
            (root / 'bin').mkdir()
            binary = root / 'bin/agentworks-vault'
            binary.write_text('#!/bin/sh\nexit 23\n')
            binary.chmod(0o755)
            calls = root / 'calls'
            script = '\n'.join([
                'set -euo pipefail',
                '. "$1"',
                'VAULT_ENABLED=true',
                # Use a globally captured log path rather than function arguments.
                'log_path="$3"',
                'systemctl() { printf "%s\\n" "$*" >> "$log_path"; }',
                'trap vault_recover EXIT',
                'vault_install "$2" "$2" agents',
            ])
            result = subprocess.run(['bash', '-c', script, 'test', str(ROOT / 'vault.sh'), str(root), str(calls)], capture_output=True)
            self.assertEqual(result.returncode, 23)
            self.assertIn('--user stop agents-vault.service', calls.read_text())
            self.assertIn('--user restart agents-vault.service', calls.read_text())

    def test_product_allowlists_match_preflight(self):
        repo = ROOT.parents[1]
        rts = repo / 'deploy/aws-ec2'
        script = (rts / 'server/build-and-activate.sh').read_text()
        runtime = (rts / 'server/runtime-config.js').read_text()
        unit = (rts / 'rootless/video-studio-agent.service').read_text()
        import re
        for expected in re.findall(r"grep -Fq '([^']+)' ", script[:script.index('RELEASE_ID=')]):
            self.assertIn(expected, runtime + unit + (rts / 'rootless/video-studio-workspace.service').read_text())
        command = 'source "$1"; for snippet in "${RUNTIME_CONFIG_REQUIRED_SNIPPETS[@]}"; do grep -Fq "$snippet" "$2" || exit 1; done'
        agents = repo / 'deploy/rootless-linux/products/agents'
        subprocess.run(['bash', '-c', command, 'test', str(agents / 'product.env'), str(agents / 'runtime-config.js')], check=True)

    def test_both_deployments_build_bootstrap_and_health_check(self):
        repo = ROOT.parents[1]
        for path in ('deploy/rootless-linux/build-and-activate.sh', 'deploy/aws-ec2/server/build-and-activate.sh'):
            script = (repo / path).read_text()
            for call in ('vault_build', 'vault_prepare', 'vault_install', 'vault_start'):
                self.assertIn(call, script)
            branch = script.index('if [[ -n "$PREBUILT" ]]; then', script.index('source "$REPO_ROOT/deploy/common/slots.sh"'))
            self.assertLess(script.index('source "$REPO_ROOT/deploy/common/vault.sh"'), branch)
            self.assertLess(script.index('vault_check_build'), script.index('vault_prepare'))
        bundle = (ROOT / 'build-release.sh').read_text()
        self.assertIn('vault_build "$REPO_ROOT" "$OUT"', bundle)
        common = (ROOT / 'vault.sh').read_text()
        self.assertLess(common.index('systemctl --user stop'), common.index('GATEWAY_BOOTSTRAP_ONLY=1'))
        self.assertIn('/healthz', common)


if __name__ == '__main__':
    unittest.main()
