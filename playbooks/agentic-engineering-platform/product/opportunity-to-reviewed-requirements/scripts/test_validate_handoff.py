#!/usr/bin/env python3
"""Exercise accepted, rejected and cross-subject artifacts for this route."""
import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate

EXAMPLES = Path(__file__).resolve().parents[1] / 'examples'
STAGES = ['discovery', 'priority', 'requirements']


class RouteContractTest(unittest.TestCase):
    def setUp(self):
        self.artifacts = [json.loads((EXAMPLES / (stage + '.json')).read_text()) for stage in STAGES]

    def test_valid_source_bound_handoff(self):
        validate(*self.artifacts)

    def test_rejects_false_outcome(self):
        self.artifacts[-1] = json.loads((EXAMPLES / ('invalid-' + STAGES[-1] + '.json')).read_text())
        with self.assertRaises(ValueError):
            validate(*self.artifacts)

    def test_rejects_wrong_subject(self):
        altered = copy.deepcopy(self.artifacts)
        altered[-1]['subject_id'] = 'another-subject'
        with self.assertRaises(ValueError):
            validate(*altered)

    def test_rejects_stale_upstream_revision(self):
        altered = copy.deepcopy(self.artifacts)
        altered[-1]['input_revision'] = 'old-revision'
        with self.assertRaises(ValueError):
            validate(*altered)


if __name__ == '__main__':
    unittest.main()
