"""Falsification checks for package-to-Crew bindings and handoff graphs."""
import json
import shutil
import tempfile
import unittest
from pathlib import Path

from validate_playbooks import ROOT, validate_crew_bindings, validate_package


SOURCE = ROOT / "agentic-engineering-platform" / "sales" / "pipeline-health-to-owned-action"


class LibraryContractTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="graph-test-", dir=SOURCE.parent)
        self.addCleanup(self.temp.cleanup)
        self.package = Path(self.temp.name)
        shutil.copytree(SOURCE, self.package, dirs_exist_ok=True)
        self.manifest_path = self.package / "playbook.json"
        self.manifest = json.loads(self.manifest_path.read_text())
        self.manifest["id"] = self.package.name
        skill_path = self.package / "SKILL.md"
        skill_path.write_text(skill_path.read_text().replace("name: pipeline-health-to-owned-action", f"name: {self.package.name}", 1))

    def check_manifest(self):
        self.manifest_path.write_text(json.dumps(self.manifest))
        errors = []
        validate_package(self.package, errors)
        return errors

    def test_rejects_handoff_cycle(self):
        self.manifest["handoffs"].append({"id": "deal-back-to-pipeline", "from": "deal", "to": "pipeline", "artifact_type": "deal-action-register/v1", "required": True})
        self.assertTrue(any("graph contains a cycle" in error for error in self.check_manifest()))

    def test_rejects_required_handoff_from_optional_slot(self):
        self.manifest["agent_slots"][0]["required"] = False
        self.assertTrue(any("cannot require an optional slot" in error for error in self.check_manifest()))

    def test_rejects_uninstalled_crew_template(self):
        errors = []
        self.manifest["agent_slots"][0]["agent_playbook_id"] = "missing-specialist"
        validate_crew_bindings(self.manifest_path, self.manifest, {"deal-follow-through-coordinator"}, errors)
        self.assertTrue(any("uninstalled Crew template 'missing-specialist'" in error for error in errors))

    def test_rejects_team_route_without_rejected_example(self):
        for example in (self.package / "examples").glob("invalid-*.json"):
            example.unlink()
        self.assertTrue(any("rejected example artifact" in error for error in self.check_manifest()))

    def test_rejects_team_route_without_contract_suite(self):
        (self.package / "scripts" / "test_validate_handoff.py").unlink()
        self.assertTrue(any("executable handoff contract suite" in error for error in self.check_manifest()))


if __name__ == "__main__":
    unittest.main()
