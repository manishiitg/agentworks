"""Exercise the shared renderer's persistent credentials and least authority."""
import importlib.util
from pathlib import Path
import os
import subprocess
import tempfile
import unittest
from unittest.mock import patch, MagicMock

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('vault_install', ROOT / 'install-vault-service.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class VaultInstallTest(unittest.TestCase):
    def test_lifecycle_keeps_healthy_unchanged_vault_and_restarts_otherwise(self):
        # #2989: exercise the real shell lifecycle, mocking only host commands
        # and the renderer/identity executable; never touch a live service.
        for answers, bootstrap_status in (([0, 0], 0), ([1], 0), ([0, 1], 0), ([1], 7)):
            with self.subTest(answers=answers, bootstrap_status=bootstrap_status), tempfile.TemporaryDirectory(dir=ROOT) as temporary:
                root = Path(temporary)
                app, build = root / 'app', root / 'build'
                (app / 'state/vault').mkdir(parents=True)
                (app / 'state/vault/service.env').write_text('EXAMPLE=value\n')
                (build / 'bin').mkdir(parents=True)
                program = build / 'bin/agentworks-vault'
                program.write_text(f'#!/bin/bash\necho bootstrap >> "$APP/commands"\nexit {bootstrap_status}\n')
                program.chmod(0o755)
                (build / 'bin/install-vault-service.py').write_text("""import json, os, sys
from pathlib import Path
root = Path(os.environ['APP'])
if '--check-running' in sys.argv:
    queue = root / 'answers'
    answers = json.loads(queue.read_text())
    status = answers.pop(0)
    queue.write_text(json.dumps(answers))
    sys.exit(status)
""")
                (app / 'answers').write_text(__import__('json').dumps(answers))
                script = r'''set -euo pipefail
source "$1/vault.sh"
export APP="$2"
VAULT_ENABLED=true
systemctl() { echo "$*" >> "$APP/commands"; }
curl() { return 0; }
vault_prepare "$3" "$APP" "$APP/docs" example 24000 24003
vault_install "$3" "$APP" example
vault_start example 24003
[[ -z "${VAULT_RESTART_PRODUCT:-}" ]]
'''
                result = subprocess.run(['bash', '-c', script, 'test', str(ROOT), str(app), str(build)], capture_output=True, text=True)
                self.assertEqual(result.returncode, bootstrap_status, result.stderr)
                commands = (app / 'commands').read_text() if (app / 'commands').exists() else ''
                if answers == [0, 0]:
                    self.assertEqual(commands, '')
                    self.assertIn('event=kept_running', result.stdout)
                else:
                    self.assertIn('stop example-vault.service', commands)
                    self.assertIn('bootstrap', commands)
                    if bootstrap_status == 0:
                        self.assertIn('restart example-vault.service', commands)
                    else:
                        self.assertNotIn('restart', commands)

    def test_reuse_only_exact_healthy_running_binary_and_config(self):
        # #2989: app-only deploy must keep Vault up, but never reuse a stale
        # executable, credential, unit, config or another process incarnation.
        with tempfile.TemporaryDirectory(dir=ROOT) as temporary:
            root = Path(temporary)
            app, build, proc = root / 'app', root / 'build', root / 'proc'
            state = app / 'state/vault'
            state.mkdir(parents=True)
            (build / 'bin').mkdir(parents=True)
            process = proc / '42'
            process.mkdir(parents=True)
            for path in (build / 'bin/agentworks-vault', process / 'exe'):
                path.write_bytes(b'vault-binary')
            for name in ('service.env', 'agent.env', 'service-token'):
                (state / name).write_text('private-fixture')
            (process / 'stat').write_text('42 (vault) ' + ' '.join(['0'] * 19 + ['123']))
            unit = f'EnvironmentFile={state}/service.env\n'
            def systemctl(args, **kwargs):
                return '42\n' if 'show' in args else unit.encode()
            response = MagicMock()
            response.__enter__.return_value.status = 200
            with patch.object(module.subprocess, 'run'), patch.object(module.subprocess, 'check_output', side_effect=systemctl), patch.object(module, 'urlopen', return_value=response):
                def reuse(record=False):
                    return module.reuse_running(app, build, 'example', 24003, record, proc)
                with self.assertRaises(FileNotFoundError):
                    reuse()  # First rollout always restarts.
                self.assertTrue(reuse(True))
                receipt = state / 'deploy-identity.json'
                self.assertEqual(receipt.stat().st_mode & 0o777, 0o600)
                self.assertNotIn('private-fixture', receipt.read_text())
                self.assertTrue(reuse())
                for name in ('service.env', 'agent.env', 'service-token'):
                    (state / name).write_text('changed')
                    self.assertFalse(reuse())
                    (state / name).write_text('private-fixture')
                unit += 'Environment=EXAMPLE=changed\n'
                self.assertFalse(reuse())
                unit = f'EnvironmentFile={state}/service.env\nEnvironmentFile=/extra.env\n'
                with self.assertRaises(ValueError):
                    reuse()
                unit = f'EnvironmentFile={state}/service.env\n'
                (process / 'stat').write_text('42 (vault) ' + ' '.join(['0'] * 19 + ['124']))
                self.assertFalse(reuse())
                (process / 'exe').write_bytes(b'different-binary')
                with self.assertRaises(ValueError):
                    reuse()
                (process / 'exe').write_bytes(b'vault-binary')
                response.__enter__.return_value.status = 503
                with self.assertRaises(ValueError):
                    reuse()
                with patch.object(module.subprocess, 'run', side_effect=subprocess.CalledProcessError(3, 'systemctl')):
                    with self.assertRaises(subprocess.CalledProcessError):
                        reuse()

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
            # #2989: atomic rewrites must be byte-identical, or every deploy
            # would invalidate the running-service identity and restart Vault.
            paths = [state / name for name in ('service-token', 'service.env', 'agent.env')]
            paths += [units / 'agents-vault.service', units / 'agents-agent.service.d/zz-vault.conf']
            first = {path: path.read_bytes() for path in paths}
            module.render(app, docs, 'agents', 24000, 24003, units)
            self.assertEqual(first, {path: path.read_bytes() for path in paths})
            for name in ('service-token', 'service.env', 'agent.env'):
                self.assertEqual((state / name).stat().st_mode & 0o777, 0o600)
            env = module.read_env(state / 'service.env')
            self.assertEqual(env['GATEWAY_AUTH_MODE'], 'platform')
            self.assertEqual(env['GATEWAY_BIND'], '127.0.0.1')
            self.assertEqual(env['GATEWAY_WORKSPACE_DIR'], str(docs / 'Chats/CapLayer'))
            self.assertNotIn('GATEWAY_UPSTREAM_URL', env)
            self.assertIn('UMask=0077', (units / 'agents-vault.service').read_text())
            self.assertIn('EnvironmentFile=', (units / 'agents-agent.service.d/zz-vault.conf').read_text())

    def test_unit_paths_are_unquoted_where_systemd_takes_quotes_literally(self):
        # systemd reads quotes in EnvironmentFile= and WorkingDirectory= as part of the path ("path is not absolute"), so the unit never started
        # (first Vault install on server B, 2026-10-04). ExecStart= may be quoted.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app, docs, units = root / 'app', root / 'docs', root / 'units'
            app.mkdir()
            module.render(app, docs, 'agents', 24000, 24003, units)
            for file in (units / 'agents-vault.service', units / 'agents-agent.service.d/zz-vault.conf'):
                for line in file.read_text().splitlines():
                    key, _, value = line.partition('=')
                    if key in ('EnvironmentFile', 'WorkingDirectory'):
                        self.assertTrue(value.startswith('/'), f'{file.name}: {line}')
                        self.assertNotIn('"', value, f'{file.name}: {line}')
            analyzer = subprocess.run(['which', 'systemd-analyze'], capture_output=True, text=True).stdout.strip()
            if analyzer:
                check = subprocess.run([analyzer, '--user', 'verify', str(units / 'agents-vault.service')], capture_output=True, text=True)
                self.assertNotIn('not absolute', check.stdout + check.stderr)

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
        # Required settings only; `grep -Fq X file && {` is a must-not-contain check.
        for expected in re.findall(r"grep -Fq '([^']+)' \"[^\"]+\" (?!&& \{)", script[:script.index('RELEASE_ID=')]):
            self.assertIn(expected, runtime + unit + (rts / 'rootless/video-studio-workspace.service').read_text())
    def test_both_deployments_build_bootstrap_and_health_check(self):
        repo = ROOT.parents[1]
        # Both deploy paths check the staged release, then the shared activation renders, bootstraps and starts Vault.
        for path in ('deploy/rootless-linux/build-and-activate.sh', 'deploy/aws-ec2/server/build-and-activate.sh'):
            script = (repo / path).read_text()
            self.assertLess(script.index('source "$REPO_ROOT/deploy/common/vault.sh"'), script.index('vault_check_build'))
            self.assertLess(script.index('vault_check_build'), script.index('\nactivate_release\n'))
        shared = (ROOT / 'activate-release.sh').read_text()
        self.assertLess(shared.index('vault_prepare'), shared.index('systemctl --user stop "$UNIT_PREFIX-agent"'))
        self.assertLess(shared.index('vault_install'), shared.index('vault_start'))
        bundle = (ROOT / 'build-release.sh').read_text()
        self.assertIn('vault_build "$REPO_ROOT" "$OUT"', bundle)
        common = (ROOT / 'vault.sh').read_text()
        self.assertLess(common.index('systemctl --user stop'), common.index('GATEWAY_BOOTSTRAP_ONLY=1'))
        self.assertIn('/healthz', common)


if __name__ == '__main__':
    unittest.main()
