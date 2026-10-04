"""The slot helper's allowed folders must contain the REAL docs root (DOCS), not the app folder's data/docs.

On RTS the docs live at /data/video-studio/docs while the app folder is /var/lib/video-studio/video-studio, so
$HOME_DIR/data/docs does not exist there: slotctl refused every command that started in the docs tree with
"the working folder is outside the allowed folders" (Crew shell tool, 2026-10-04).
"""
import re
import unittest
from pathlib import Path

SCRIPT = (Path(__file__).resolve().parent / "provision-slots.sh").read_text()


class ProvisionSlotsConfigTest(unittest.TestCase):
    def test_allowed_cwd_contains_the_docs_root(self):
        match = re.search(r'"allowed_cwd":\s*\[([^\]]*)\]', SCRIPT)
        self.assertIsNotNone(match, "allowed_cwd not found in the generated slotctl config")
        entries = [e.strip().strip('"') for e in match.group(1).split(",")]
        self.assertIn("$DOCS", entries)
        self.assertNotIn("$HOME_DIR/data/docs", entries)

    def test_docs_root_and_allowed_cwd_use_the_same_variable(self):
        docs_root = re.search(r'"docs_root":\s*"([^"]+)"', SCRIPT)
        self.assertIsNotNone(docs_root)
        self.assertEqual(docs_root.group(1), "$DOCS")

    def test_default_docs_root_is_unchanged_for_the_shared_hosts(self):
        # Excellence/Confida/SparkQuill do not pass DOCS: the default must keep them where they were.
        self.assertIn('DOCS="${DOCS:-$HOME_DIR/data/docs}"', SCRIPT)


if __name__ == "__main__":
    unittest.main()
