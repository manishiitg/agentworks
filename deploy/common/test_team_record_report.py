"""#2809: a duplicate inventory must not mutate records or promote names to identity."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import uuid
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("team_report", Path(__file__).resolve().parents[2] / "scripts/team-record-report.py")
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class TeamRecordReportTest(unittest.TestCase):
    def test_read_only_twins_names_and_orphans_keep_private_contents_out(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            state, workspace = root / "state", root / "data"
            def save(path, value):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(json.dumps(value))
            home = str(uuid.uuid5(uuid.NAMESPACE_OID, "agentworks/team-home/source"))
            ids = ["source", "archived", "other-owner", home, "orphan"]
            owners = {"source": "owner", "archived": "owner", "other-owner": "other", home: "team-source", "orphan": "team-missing"}
            save(state / "ownership/projects.json", {"schema_version": 1, "projects": {"work/"+pid: {"product": "work", "project_id": pid, "folder": pid, "owner_id": owners[pid]} for pid in ids}})
            for pid in ids:
                save(workspace / "Crew" / pid / "product.json", {"title": "Same name"})
            for pid in ["source", "archived", "other-owner"]:
                save(state / "crew-sync" / (pid+".json"), {"side": "server", "pair_id": pid+"-pair", "mac_id": "same-mac", "peer_project_id": "same-source", "archive_only": pid == "archived"})
            save(state / "team-folders/source/team.json", {"project_id": "source", "owner_id": "owner", "online_home": home, "people": [{"user_id": "member"}]})
            save(state / "team-folders" / home / "team.json", {"project_id": home, "owner_id": "team-source", "roster_source": "source", "people": [{"user_id": "owner"}]})
            save(state / "team-folders/orphan/team.json", {"project_id": "orphan", "owner_id": "team-missing", "roster_source": "missing"})
            save(state / "team-folders/unregistered/team.json", {"project_id": "unregistered", "owner_id": "owner", "people": [{"user_id": "member"}]})
            save(workspace / "_users/owner/chat_history/product-conversations.json", {"entries": {"work/source:chat:online": {"profile_id": "work", "conversation_key": "source:chat:online", "conversation_id": "agent", "updated_at": "2026-01-01T00:00:00Z", "native_session_id": "NEVER-PRINT-THIS"}}, "agents": {"work/source:chat:online": {"name": "Helper · online", "purpose": "NEVER-PRINT-THIS"}}})
            directory = root / "directory.json"
            save(directory, {"users": [{"user_id": "team-source", "username": "Team: Example"}, {"user_id": "team-missing", "username": "Team: Example (10)"}]})
            # Contents/database values are not audit input. Counting stats is allowed.
            private = workspace / "Crew/source/db/db.sqlite"
            private.parent.mkdir()
            private.write_text("NEVER-PRINT-THIS")
            (workspace / "Crew/source/link").symlink_to(root / "outside")
            before = {str(p): (hashlib.sha256(p.read_bytes()).hexdigest(), p.stat().st_mtime_ns) for p in root.rglob("*") if p.is_file() and not p.is_symlink()}
            original = audit.os.open
            def guarded(path, flags, *args, **kwargs):
                self.assertNotIn("sqlite", str(path))
                self.assertEqual(flags & (audit.os.O_WRONLY | audit.os.O_RDWR), 0)
                return original(path, flags, *args, **kwargs)
            with patch.object(audit.os, "open", side_effect=guarded):
                result = audit.report(state, workspace, "example", directory)
            after = {str(p): (hashlib.sha256(p.read_bytes()).hexdigest(), p.stat().st_mtime_ns) for p in root.rglob("*") if p.is_file() and not p.is_symlink()}
            self.assertEqual(before, after)
            self.assertNotIn("NEVER-PRINT-THIS", json.dumps(result))
            twins = next(g for g in result["groups"] if g["kind"] == "crew_twins")
            self.assertEqual(set(twins["project_ids"]), {"source", "archived"})
            self.assertEqual(twins["canonical_project_id"], "source")
            rows = {r["id"]: r for r in result["projects"]}
            self.assertEqual(rows[home]["canonical"]["project_id"], "source")
            self.assertEqual(rows["unregistered"]["files"]["count"], None)
            self.assertEqual(rows["orphan"]["findings"][0]["kind"], "orphan_or_conflicting_home")
            self.assertEqual(rows["source"]["findings"][0]["kind"], "online_agent_name_candidate")
            self.assertIsNone(next(g for g in result["groups"] if g["kind"] == "numbered_team_accounts")["canonical_account_id"])
            # A second live binding is ambiguous, not a newest-wins cleanup choice.
            save(state / "crew-sync/archived.json", {"side": "server", "pair_id": "new", "mac_id": "same-mac", "peer_project_id": "same-source"})
            again = audit.report(state, workspace, "example", directory)
            self.assertIsNone(next(g for g in again["groups"] if g["kind"] == "crew_twins")["canonical_project_id"])
            save(directory, {"users": [{"id": "someone", "password_hash": "NEVER-PRINT-THIS"}]})
            with self.assertRaisesRegex(ValueError, "sanitized"):
                audit.report(state, workspace, "example", directory)


if __name__ == "__main__":
    unittest.main()
