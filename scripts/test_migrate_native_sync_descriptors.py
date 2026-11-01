"""PLAT-856-442: an existing new stage must survive conversion of an old one."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class NativeDescriptorMigrationTest(unittest.TestCase):
    def test_duplicate_stage_is_archived_and_foreign_session_is_refused(self):
        for converted, foreign in [(False, False), (True, False), (False, True)]:
            with self.subTest(converted=converted, foreign=foreign), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                crew = root / "_system/crew-data/test-project"
                native = "12345678-1234-4234-8234-123456789012"
                old = crew / "native-sync" / native
                current = old.with_name("muse-cli-" + native)
                old.mkdir(parents=True)
                current.mkdir()
                legacy = {"session_id": native, "files": {"journal.jsonl": {"size": 3}}}
                value = dict(legacy, provider="muse-cli", format="muse-jsonl", transport="structured")
                (old / "manifest.json").write_text(json.dumps(value if converted else legacy))
                (old / "journal.jsonl").write_text("old")
                admitted = dict(value, session_id="other-session") if foreign else value
                (current / "manifest.json").write_text(json.dumps(admitted))
                (current / "journal.jsonl").write_text("newer completed bytes")
                original = (old / "manifest.json").read_bytes()
                result = subprocess.run([sys.executable, str(Path(__file__).with_name("migrate-native-sync-descriptors.py")), str(root), "--crew", "test-project"], capture_output=True, text=True)
                self.assertEqual((current / "journal.jsonl").read_text(), "newer completed bytes")
                self.assertEqual(json.loads((current / "manifest.json").read_text()), admitted)
                if foreign:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("different session", result.stderr)
                    self.assertEqual((old / "manifest.json").read_bytes(), original)
                    self.assertFalse(list(crew.glob("native-sync.before-*")))
                else:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    archived = next(crew.glob("native-sync.before-*/" + native))
                    self.assertEqual((archived / "manifest.json").read_bytes(), original)
                    self.assertEqual((archived / "journal.jsonl").read_text(), "old")
                    self.assertFalse(old.exists())
                    repeated = subprocess.run(result.args, capture_output=True, text=True)
                    self.assertEqual(repeated.returncode, 0, repeated.stderr)


if __name__ == "__main__":
    unittest.main()
