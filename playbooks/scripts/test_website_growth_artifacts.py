"""Contract examples must show a valid first route and reject broken handoffs."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import unittest
from pathlib import Path


PACKAGE = Path(__file__).resolve().parents[1] / "agentic-engineering-platform/website-growth/website-growth-loop"
SCRIPT = PACKAGE / "scripts/validate_growth_artifact.py"
SPEC = importlib.util.spec_from_file_location("validate_growth_artifact", SCRIPT)
assert SPEC and SPEC.loader
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)


class GrowthArtifactTest(unittest.TestCase):
    def setUp(self) -> None:
        self.brief = json.loads((PACKAGE / "examples/growth-priority-brief.json").read_text())
        self.search = json.loads((PACKAGE / "examples/search-opportunity-list.json").read_text())

    def test_worked_two_crew_handoff(self) -> None:
        self.assertIn("F-001", module.validate_brief(self.brief))
        module.validate_search(self.search, self.brief)

    def test_bad_search_fixture_blocks_handoff(self) -> None:
        bad = json.loads((PACKAGE / "examples/invalid-search-opportunity-list.json").read_text())
        with self.assertRaises(module.InvalidArtifact):
            module.validate_search(bad, self.brief)

    def test_unknown_finding_blocks_strategist_artifact(self) -> None:
        self.brief["priorities"][0]["evidence_refs"] = ["F-404"]
        with self.assertRaisesRegex(module.InvalidArtifact, "unknown finding"):
            module.validate_brief(self.brief)

    def test_unknown_finding_blocks_search_artifact(self) -> None:
        self.search["buyer_questions"][0]["source_finding_id"] = "F-404"
        with self.assertRaisesRegex(module.InvalidArtifact, "unknown strategist finding"):
            module.validate_search(self.search, self.brief)

    def test_unattributed_search_demand_is_rejected(self) -> None:
        self.search["buyer_questions"][0]["search_demand"] = "5000 monthly searches"
        with self.assertRaisesRegex(module.InvalidArtifact, "demand_source"):
            module.validate_search(self.search, self.brief)

    def test_missing_page_observation_is_rejected(self) -> None:
        self.brief["findings"][0]["observation"] = ""
        with self.assertRaisesRegex(module.InvalidArtifact, "observation"):
            module.validate_brief(self.brief)

    def test_cli_requires_the_strategist_brief_for_search(self) -> None:
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "search", str(PACKAGE / "examples/search-opportunity-list.json")],
            capture_output=True, text=True, check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requires --brief", result.stderr)


if __name__ == "__main__":
    unittest.main()
