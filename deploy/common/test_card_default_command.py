import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "card_default", Path(__file__).resolve().parents[2] / "scripts" / "migrate-card-default-command.py")
card = importlib.util.module_from_spec(spec)
spec.loader.exec_module(card)


class CardDefaultCommandMigrationTest(unittest.TestCase):
    def test_adds_once_and_skips_marker_or_existing_command(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name in ("fresh", "deleted", "has-it"):
                (root / "Crew" / name).mkdir(parents=True)
            (root / "Crew/deleted/commands/custom").mkdir(parents=True)
            (root / "Crew/deleted/commands/custom" / card.MARKER).write_text("seeded\n")
            (root / "Crew/has-it/commands/custom" / card.NAME).mkdir(parents=True)
            self.assertEqual(card.migrate(root, check=True)["changed_crews"], 1)
            self.assertFalse((root / "Crew/fresh/commands").exists(), "check mode must write nothing")
            self.assertEqual(card.migrate(root)["changed_crews"], 1)
            command = (root / "Crew/fresh/commands/custom" / card.NAME / "COMMAND.md").read_text()
            self.assertIn("show_on_card: true", command)
            self.assertIn(card.MESSAGE, command)
            self.assertFalse((root / "Crew/deleted/commands/custom" / card.NAME).exists())
            self.assertFalse((root / "Crew/has-it/commands/custom" / card.MARKER).exists())
            # Deleting the default after the run does not bring it back.
            import shutil
            shutil.rmtree(root / "Crew/fresh/commands/custom" / card.NAME)
            self.assertEqual(card.migrate(root)["changed_crews"], 0)


if __name__ == "__main__":
    unittest.main()
