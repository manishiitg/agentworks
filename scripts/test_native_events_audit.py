"""PR1922 regression: batch logs must not erase event or missing-reply checks."""
import contextlib
import importlib.util
import io
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("native_events_audit", Path(__file__).with_name("native-events-audit.py"))
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class NativeEventAuditTest(unittest.TestCase):
    def test_batch_logs_keep_metadata_and_missing_reply_checks_without_text(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            journal = root / "structured-chat-events-v2.sqlite"
            private_text = "private message must never enter an operational report"
            with sqlite3.connect(journal) as db:
                db.execute("CREATE TABLE structured_chat_events(event_id TEXT, payload BLOB)")
                for event_id, record in [
                    ("tool", {"kind": "tool", "state": "finished"}),
                    ("missing", {"kind": "helper", "state": "finished", "followup_turn_id": "missing-turn"}),
                    ("saved", {"kind": "helper", "state": "finished", "followup_turn_id": "saved-turn"}),
                ]:
                    record.update(provider="codex-cli", native_session_id="native", text=private_text)
                    payload = {"type": "coding_agent_native_activity", "data": {"data": {"record": record}}}
                    db.execute("INSERT INTO structured_chat_events VALUES(?,?)", (event_id, json.dumps(payload)))
            db.close()
            log = root / "agent.log"
            log.write_text("[NATIVE_RECORD] batch count=3 first=tool last=saved cursor_before=0 cursor_after=3\n"
                           "[NATIVE_RECORD] reply_saved native=native turn=saved-turn\n")
            config = {"name": "example", "state_root": str(root), "log_path": str(log)}

            def probe():
                output = io.StringIO()
                with patch("sys.argv", ["probe", json.dumps(config)]), patch("shutil.which", return_value=None), contextlib.redirect_stdout(output):
                    exec(audit.PROBE, {})
                self.assertNotIn(private_text, output.getvalue())
                return json.loads(output.getvalue())

            report = probe()
            self.assertEqual(report["native_event_status"], "AVAILABLE")
            self.assertEqual(report["observed_events"], {"codex-cli:tool:finished": 1, "codex-cli:helper:finished": 2})
            self.assertEqual([row["native_turn_id"] for row in report["followups_without_saved_reply"]], ["missing-turn"])
            journal.unlink()
            missing = probe()
            self.assertEqual(missing["native_event_status"], "UNAVAILABLE")
            self.assertIsNone(missing["observed_events"])
            self.assertIsNone(missing["followups_without_saved_reply"])


if __name__ == "__main__":
    unittest.main()
