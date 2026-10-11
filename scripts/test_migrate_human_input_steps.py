"""Exercise the real drained-deploy migration without executing workflows."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("migration", Path(__file__).with_name("migrate_human_input_steps.py"))
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class MigrationTest(unittest.TestCase):
    def test_reviewed_replacement_is_guarded_and_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            root, recipes = Path(directory) / "workspace", Path(directory) / "recipes"
            relative = Path("Workflow/example/planning/plan.json")
            path, recipe_path = root / relative, recipes / relative
            path.parent.mkdir(parents=True)
            recipe_path.parent.mkdir(parents=True)
            before = json.dumps({"steps": [{"id": "ask", "type": "human_input"}]}).encode()
            path.write_bytes(before)
            config_path = path.with_name("step_config.json")
            config_path.write_text('{"steps":[{"step_id":"ask"}]}')
            config_before = config_path.read_bytes()
            recipe = {"source_sha256": hashlib.sha256(before).hexdigest(), "plan": {"steps": [{"id": "work", "type": "agent", "required_variables": ["MONTH"]}]}, "step_config": {"steps": []}, "step_config_sha256": hashlib.sha256(config_before).hexdigest(), "variables_add": [{"name": "MONTH", "description": "Required month"}]}
            recipe_path.write_text(json.dumps(recipe))
            self.assertEqual(migration.migrate(root, recipes), 3)
            self.assertEqual(path.read_bytes(), before)
            config_path.write_text('{"steps":[]}')
            with self.assertRaisesRegex(ValueError, "configuration changed"):
                migration.migrate(root, recipes, True)
            self.assertEqual(path.read_bytes(), before)
            config_path.write_bytes(config_before)
            historical = root / "Workflow/example/runs/iteration-1/planning/plan.json"
            historical.parent.mkdir(parents=True)
            historical.write_bytes(before)
            self.assertEqual(migration.migrate(root, recipes, True), 3)
            self.assertEqual(historical.read_bytes(), before)
            self.assertEqual(path.with_name("plan.json.pre-human-input-retirement.bak").read_bytes(), before)
            self.assertEqual(migration.migrate(root, recipes, True), 0)


if __name__ == "__main__":
    unittest.main()
