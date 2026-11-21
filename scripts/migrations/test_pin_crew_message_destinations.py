"""PLAT-746-885: stored defaults become physical targets without changing work."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("pin_destinations", Path(__file__).with_name("pin-crew-message-destinations.py"))
pin = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pin)


class PinDestinationsTest(unittest.TestCase):
    def test_backup_rewrite_and_repeat_preserve_explicit_and_isolated_targets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            crew = root / "crew"
            crew.mkdir()
            key = "example:chat:original"
            (crew / "product.json").write_text(json.dumps({"product": "work", "id": "example", "main_chat_key": key}))
            registry = root / "registry.json"
            registry.write_text(json.dumps({"entries": {"work/" + key: {
                "profile_id": "work", "conversation_key": key,
                "session_id": "session", "workspace_path": str(crew.resolve())}}}))
            rows = []
            for identity, action, inputs in [
                ("default", {"type": "message"}, {"messages": ["hello"]}),
                ("chosen", {"type": "message", "tab": "example:chat:chosen"}, {}),
                ("isolated", {"type": "message", "conversation": "isolated"}, {}),
                ("script", {"type": "run", "workflow_id": "script"}, {}),
                ("watcher", {"type": "message"}, {"condition": {"target": {"kind": "agent", "chat_id": "main"}}}),
            ]:
                rows.append({"id": identity, "project": {"kind": "crew", "id": "example"}, "action": action, "input": inputs, "enabled": False})
            file = crew / "schedules.json"
            original = json.dumps({"schedules": rows})
            file.write_text(original)
            executable = root / "script"
            executable.mkdir()
            script_file = executable / "workflow.json"
            script_original = json.dumps({"schedules": [{"chat_id": "main", "enabled": False}, {"chat_id": ""}]})
            script_file.write_text(script_original)
            # Real client failure: canonical script function has no workflow.json.
            canonical = root / "canonical-script"
            canonical.mkdir()
            function_file = canonical / "functions.json"
            function_rows = [
                {"id": "invocation", "enabled": True, "action": {"type": "run", "tab": "main"}},
                {"id": "chosen", "enabled": False, "action": {"type": "run", "tab": "example:chat:chosen"}},
                {"id": "blank", "action": {"type": "run"}},
                {"id": "other", "action": {"type": "message", "tab": "main"}},
            ]
            function_original = json.dumps({"functions": function_rows})
            function_file.write_text(function_original)
            timer_file = canonical / "schedules.json"
            timer_file.write_text(json.dumps({"schedules": [function_rows[0]]}))
            changes = pin.inspect(crew, registry, executable_roots=[executable, canonical])
            pin.apply(changes, root / "backup")
            backups = json.loads((root / "backup/files.json").read_text())
            self.assertEqual((root / "backup" / backups[str(file)]).read_text(), original)
            saved = json.loads(file.read_text())["schedules"]
            self.assertEqual(saved[0]["action"]["tab"], key)
            self.assertEqual(saved[0]["input"], rows[0]["input"])
            self.assertEqual(saved[1:4], rows[1:4])
            self.assertEqual(saved[4]["input"]["condition"]["target"]["chat_id"], "original")
            self.assertTrue(all(not row["enabled"] for row in saved))
            self.assertEqual(pin.inspect(crew, registry, executable_roots=[executable, canonical]), [])
            self.assertEqual((root / "backup/functions.json").read_text(), function_original)
            functions = json.loads(function_file.read_text())["functions"]
            self.assertEqual(functions[0], {**function_rows[0], "action": {"type": "run", "tab": key}})
            self.assertEqual(functions[1:], function_rows[1:])
            self.assertEqual(json.loads(timer_file.read_text())["schedules"][0]["action"]["tab"], key)
            self.assertEqual((root / "backup/workflow.json").read_text(), script_original)
            self.assertEqual(json.loads(script_file.read_text())["schedules"], [{"chat_id":key,"enabled":False},{"chat_id":""}])
            registry.write_text('{"entries":{}}')
            with self.assertRaises(ValueError):
                pin.inspect(crew, registry)


if __name__ == "__main__":
    unittest.main()
