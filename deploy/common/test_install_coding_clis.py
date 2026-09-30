"""Offline deployment provisioning checks using disposable fake vendor releases."""
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent


class CodingCLIInstallTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.prefix = self.root / 'tools'
        self.home = self.root / 'home'
        self.fake = self.root / 'fake-bin'
        self.fake.mkdir()
        self.env = {**os.environ, 'PATH': f'{self.fake}:{os.environ["PATH"]}', 'CLI_FIXTURES': str(self.root)}
        payload = io.BytesIO()
        binary = b'#!/bin/sh\necho 1.2.14\n'
        with tarfile.open(fileobj=payload, mode='w:gz') as archive:
            item = tarfile.TarInfo('antigravity'); item.size = len(binary); item.mode = 0o755
            archive.addfile(item, io.BytesIO(binary))
        (self.root / 'payload').write_bytes(payload.getvalue())
        (self.root / 'manifest.json').write_text(json.dumps({
            'version': '1.2.14',
            'url': 'https://storage.googleapis.com/antigravity-public/test/cli.tar.gz',
            'sha512': hashlib.sha512(payload.getvalue()).hexdigest(),
        }))
        self.tool('uname', '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        self.tool('timeout', '#!/bin/sh\nshift\nexec "$@"\n')
        self.tool('npm', '''#!/usr/bin/env python3
import os,pathlib,sys
root=pathlib.Path(os.environ['CLI_FIXTURES']);args=sys.argv[1:]
prefix=pathlib.Path(args[args.index('--prefix')+1]);(prefix/'bin').mkdir(parents=True,exist_ok=True)
with (root/'npm-calls').open('a') as f:f.write(' '.join(args)+'\\n')
for name in ('claude','codex','pi'):
 p=prefix/'bin'/name;p.write_text('#!/bin/sh\\necho 1.2.14\\n'+('exit 47\\n' if name=='codex' and os.environ.get('FAIL_CODEX') else ''));p.chmod(0o755)
''')
        self.tool('curl', '''#!/usr/bin/env python3
import os,pathlib,sys
root=pathlib.Path(os.environ['CLI_FIXTURES']);args=sys.argv[1:];url=next(x for x in args if x.startswith('https://'))
with (root/'curl-calls').open('a') as f:f.write(url+'\\n')
if 'cursor.com' in url:
 content=b'#!/bin/sh\\nmkdir -p "$HOME/.local/bin"\\nprintf "#!/bin/sh\\\\necho 1.2.14\\\\n" > "$HOME/.local/bin/cursor-agent"\\nchmod +x "$HOME/.local/bin/cursor-agent"\\n'
elif 'dev.meta.ai' in url:
 content=b'#!/bin/sh\\nmkdir -p "$MUSE_INSTALL_DIR"\\nprintf "#!/bin/sh\\\\necho 1.2.14\\\\n" > "$MUSE_INSTALL_DIR/muse"\\nchmod +x "$MUSE_INSTALL_DIR/muse"\\n'
else:content=(root/('manifest.json' if 'manifests/' in url else 'payload')).read_bytes()
if '-o' in args:pathlib.Path(args[args.index('-o')+1]).write_bytes(content)
else:sys.stdout.buffer.write(content)
''')
        self.tool('sha512sum', '''#!/usr/bin/env python3
import hashlib,pathlib,sys
expected,path=sys.stdin.read().strip().split(None,1)
sys.exit(0 if hashlib.sha512(pathlib.Path(path).read_bytes()).hexdigest()==expected else 1)
''')

    def tool(self, name, source):
        path = self.fake / name; path.write_text(source); path.chmod(0o755)

    def run_installer(self, mode='auto'):
        return subprocess.run(['bash', str(ROOT/'install-coding-clis.sh'), str(self.prefix), str(self.home), mode],
                              env=self.env, capture_output=True, text=True, timeout=30)

    def test_installs_missing_clis_and_updates_all_existing_clis(self):
        for _ in range(2):
            result = self.run_installer()
            self.assertEqual(result.returncode, 0, result.stderr)
        for name in ('claude', 'codex', 'cursor-agent', 'pi', 'muse', 'agy'):
            self.assertTrue((self.prefix/'bin'/name).is_file(), name)
        calls = (self.root/'npm-calls').read_text().splitlines()
        self.assertEqual(len(calls), 2)
        for call in calls:
            for package in ('@anthropic-ai/claude-code@latest', '@openai/codex@latest', '@earendil-works/pi-coding-agent@latest'):
                self.assertIn(package, call)
        self.assertEqual((self.root/'curl-calls').read_text().count('https://cursor.com/install'), 2)
        self.assertFalse((self.home/'.gemini/antigravity-cli/settings.json').exists())

    def test_gemini_mode_preserves_other_settings_without_storing_key(self):
        settings = self.home/'.gemini/antigravity-cli/settings.json'
        settings.parent.mkdir(parents=True); settings.write_text('{"theme":"dark"}')
        result = self.run_installer('gemini')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(settings.read_text()), {'theme': 'dark', 'modelProvider': 'gemini'})
        self.assertEqual(settings.stat().st_mode & 0o777, 0o600)

    def test_bad_checksum_never_replaces_existing_agy(self):
        target = self.prefix/'bin/agy';target.parent.mkdir(parents=True);target.write_text('original')
        (self.root/'payload').write_bytes(b'corrupted download')
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(target.read_text(), 'original')

    def test_published_package_without_working_binary_fails_deployment(self):
        self.env['FAIL_CODEX'] = '1'
        self.assertNotEqual(self.run_installer().returncode, 0)

    def test_installer_catalog_matches_offered_providers(self):
        catalog = (ROOT/'install-coding-clis.sh').read_text()
        binaries = re.search(r'CODING_CLI_BINARIES=\(([^)]+)\)', catalog).group(1).split()
        provider_names = {'claude': 'claude-code', 'codex': 'codex-cli', 'cursor-agent': 'cursor-cli', 'pi': 'pi-cli', 'muse': 'muse-cli', 'agy': 'agy-cli'}
        source = (ROOT.parents[1]/'agent_go/cmd/server/llm_config_handlers.go').read_text()
        offered = re.findall(r'"([^"]+)"', re.search(r'var supportedLLMProviders = \[\]string\{([^}]+)\}', source).group(1))
        self.assertCountEqual([provider_names[x] for x in binaries], offered)


if __name__ == '__main__':
    unittest.main()
