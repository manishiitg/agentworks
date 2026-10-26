import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location(
    "repair", Path(__file__).with_name("repair-workflow-builder-tabs.py"))
repair = importlib.util.module_from_spec(spec)
spec.loader.exec_module(repair)


class BuilderTabRepairTest(unittest.TestCase):
    def test_execution_refs_removed_without_deleting_history_or_other_tabs(self):
        # PLAT-746-149: an owner-authorized execution is not a Builder tab.
        with tempfile.TemporaryDirectory() as temp:
            root, backup = Path(temp) / "docs", Path(temp) / "backup"
            for prefix in (Path(), Path("_users/person")):
                registry = root / prefix / "chat_history/product-conversations.json"
                registry.parent.mkdir(parents=True)
                entries = {
                    "builder": {"profile_id": "agentworks", "workspace_path": "Workflow/example", "session_id": "interactive"},
                    "crew": {"profile_id": "work", "workspace_path": "Crew/example", "session_id": "pat-example-run-1"},
                }
                for session in ("pat-example-run-1", "run-1", "wfask-1", "schedule-1", "bot-1"):
                    entries["agentworks/example:chat:" + session] = {"profile_id": "agentworks", "workspace_path": "Workflow/example", "resource_id": "example", "conversation_key": "example:chat:" + session, "title": "Builder", "session_id": session}
                original = {"version": 1, "entries": entries, "previous": {"old": "kept"}, "agents": {"named": "kept"}}
                registry.write_text(json.dumps(original))
                transcript = registry.with_name("session-pat-example-run-1.json")
                transcript.write_text("retained history")
            before = {p.relative_to(root): p.read_bytes() for p in root.rglob("*.json")}
            self.assertEqual(repair.repair(root, check=True)["run_tab_refs_removed"], 2)
            self.assertEqual(before, {p.relative_to(root): p.read_bytes() for p in root.rglob("*.json")})
            self.assertEqual(repair.repair(root, backup)["run_tab_refs_removed"], 2)
            for relative, content in before.items():
                path = root / relative
                if path.name != "product-conversations.json":
                    self.assertEqual(path.read_bytes(), content)
                    continue
                self.assertEqual((backup / relative).read_bytes(), content)
                expected = json.loads(content)
                expected["entries"] = {k: v for k, v in expected["entries"].items() if v["session_id"] != "pat-example-run-1" or k == "crew"}
                self.assertEqual(json.loads(path.read_text()), expected)
            self.assertEqual(repair.repair(root, backup)["registries_changed"], 0)


if __name__ == "__main__":
    unittest.main()
