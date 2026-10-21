import importlib.util
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest


spec = importlib.util.spec_from_file_location(
    "cleanup", Path(__file__).with_name("remove-crew-pulse-tables.py"))
cleanup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cleanup)


class CrewPulseCleanupTest(unittest.TestCase):
    def test_cleanup_preserves_real_rows_and_goals_and_backs_up(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docs = root / "docs"
            for product in ["Crew", "Workflow"]:
                folder = docs / product / "demo"
                (folder / "db").mkdir(parents=True)
                (folder / "product.json").write_text(json.dumps({"product": "work"}))
                with closing(sqlite3.connect(folder / "db/db.sqlite")) as db:
                    db.executescript("""
                        CREATE TABLE pulse_reviews (id TEXT);
                        CREATE TABLE pulse_schema_migrations (id TEXT);
                        INSERT INTO pulse_schema_migrations VALUES ('v1');
                        CREATE TABLE pulse_issues (id TEXT);
                        INSERT INTO pulse_issues VALUES ('keep');
                        CREATE TABLE business_data (id TEXT);
                        INSERT INTO business_data VALUES ('keep');
                    """)
            preview = cleanup.cleanup(docs)
            self.assertEqual(preview["affected_count"], 1)
            self.assertEqual(preview["dropped_count"], 0)
            applied = cleanup.cleanup(docs, root / "backup")
            self.assertFalse(applied["errors"])
            self.assertEqual(applied["dropped_count"], 2)
            self.assertEqual(applied["databases"][0]["retained_nonempty"], {"pulse_issues": 1})
            with closing(sqlite3.connect(docs / "Crew/demo/db/db.sqlite")) as db:
                self.assertNotIn("pulse_reviews", cleanup.tables(db))
                self.assertEqual(db.execute("SELECT * FROM business_data").fetchall(), [("keep",)])
                self.assertEqual(db.execute("SELECT * FROM pulse_issues").fetchall(), [("keep",)])
            for path in [root / "backup/Crew/demo/db/db.sqlite", docs / "Workflow/demo/db/db.sqlite"]:
                with closing(sqlite3.connect(path)) as db:
                    self.assertIn("pulse_reviews", cleanup.tables(db))
                    self.assertEqual(db.execute("SELECT * FROM pulse_schema_migrations").fetchall(), [("v1",)])
            self.assertEqual(cleanup.cleanup(docs)["affected_count"], 0)


if __name__ == "__main__":
    unittest.main()
