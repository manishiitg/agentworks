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
                "session_id": "session", "workspace_path": str(crew)}}}))
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
            changes = pin.inspect(crew, registry)
            pin.apply(changes, root / "backup")
            self.assertEqual((root / "backup/schedules.json").read_text(), original)
            saved = json.loads(file.read_text())["schedules"]
            self.assertEqual(saved[0]["action"]["tab"], key)
            self.assertEqual(saved[0]["input"], rows[0]["input"])
            self.assertEqual(saved[1:4], rows[1:4])
            self.assertEqual(saved[4]["input"]["condition"]["target"]["chat_id"], "original")
            self.assertTrue(all(not row["enabled"] for row in saved))
            self.assertEqual(pin.inspect(crew, registry), [])
            registry.write_text('{"entries":{}}')
            with self.assertRaises(ValueError):
                pin.inspect(crew, registry)


if __name__ == "__main__":
    unittest.main()
