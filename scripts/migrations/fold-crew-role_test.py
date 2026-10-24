"""Folding the role into the purpose keeps both words once, backs up what it changes, and is safe to repeat."""
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name('fold-crew-role.py')


def write(root, folder, manifest):
    path = root / 'Crew' / folder / 'product.json'
    path.parent.mkdir(parents=True)
    path.write_text(json.dumps(manifest))
    return path


class FoldCrewRole(unittest.TestCase):
    def test_fold_backup_and_second_run(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            both = write(root, 'both', {'description': 'Plan the Q3 launch.', 'identity': {'name': 'A', 'role': 'Launch partner'}})
            only_role = write(root, 'only-role', {'identity': {'role': 'Reviewer'}})
            repeats = write(root, 'repeats', {'description': 'Reviewer of every release.', 'identity': {'role': 'reviewer'}})
            untouched = write(root, 'untouched', {'description': 'Keep.', 'identity': {'name': 'B'}})
            before = untouched.read_text()
            run = lambda: subprocess.run([sys.executable, str(SCRIPT), '--workspace-root', folder, '--state-root', folder + '/state'], check=True, capture_output=True, text=True).stdout
            self.assertIn('folded the role of 3', run())
            self.assertEqual(json.loads(both.read_text()), {'description': 'Launch partner. Plan the Q3 launch.', 'identity': {'name': 'A'}})
            self.assertEqual(json.loads(only_role.read_text()), {'description': 'Reviewer'})
            self.assertEqual(json.loads(repeats.read_text()), {'description': 'Reviewer of every release.'})
            self.assertEqual(untouched.read_text(), before)
            backups = list((root / 'state/migrations').glob('fold-crew-role-backup-*/*.product.json'))
            self.assertEqual(len(backups), 3)
            self.assertIn('folded the role of 0', run())


if __name__ == '__main__':
    unittest.main()
