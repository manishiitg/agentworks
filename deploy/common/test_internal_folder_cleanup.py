"""#2825: preview is inert; cleanup backs up config and never deletes folders."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
SCRIPT = Path(__file__).resolve().parents[2] / 'scripts/cleanup-internal-folder-grants.py'

class InternalFolderCleanupTest(unittest.TestCase):
    def test_chrome_group_reuses_only_same_window_display_name(self):
        subprocess.run(['node', str(SCRIPT.parent/'test-chrome-group-reuse.mjs')], check=True)

    def test_preview_backup_and_preserve_ordinary_grants(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            runtime = root/'cli-runtimes/v1/old'
            runtime.mkdir(parents=True)
            source = root/'work-folder-access.json'
            source.write_text(json.dumps({'assignments': {'owner': {'roots': [str(root)]}}, 'grants': {'owner': [{'id':'old','path':str(runtime)}, {'id':'work','path':str(root/'ordinary-missing')} ]}}))
            before = source.read_bytes()
            subprocess.run(['python3',str(SCRIPT),str(source)],check=True,capture_output=True)
            self.assertEqual(source.read_bytes(),before)
            subprocess.run(['python3',str(SCRIPT),str(source),'--apply'],check=True,capture_output=True)
            self.assertEqual(list(root.glob('*.backup-*'))[0].read_bytes(),before)
            self.assertEqual(json.loads(source.read_text())['grants']['owner'][0]['id'],'work')
            self.assertTrue(runtime.exists())

if __name__=='__main__': unittest.main()
