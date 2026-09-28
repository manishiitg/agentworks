import copy
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("experiment_handoff", Path(__file__).with_name("validate_handoff.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def example(name):
    return json.loads((ROOT / "examples" / name).read_text())


class ExperimentHandoffTests(unittest.TestCase):
    def setUp(self):
        self.plan = example("frozen-experiment-plan.json")
        self.execution = example("experiment-execution-record.json")
        self.readout = example("experiment-inconclusive-readout.json")

    def test_provider_confirmed_launch_and_honest_readouts(self):
        MODULE.validate_execution(self.plan, self.execution)
        for name in ("experiment-inconclusive-readout.json", "experiment-measured-readout.json", "experiment-pending-window.json"):
            MODULE.validate_readout(self.plan, self.execution, example(name))

    def test_pending_approval_blocks_outcome(self):
        pending = example("experiment-pending-approval.json")
        MODULE.validate_execution(self.plan, pending)
        with self.assertRaisesRegex(ValueError, "pending execution blocks"):
            MODULE.validate_readout(self.plan, pending, self.readout)

    def test_false_launch_and_revision_drift_are_blocked(self):
        with self.assertRaisesRegex(ValueError, "provider-confirmed launched"):
            MODULE.validate_execution(self.plan, example("invalid-experiment-execution.json"))
        wrong = copy.deepcopy(self.execution)
        wrong["approval"]["plan_revision"] = "rev2"
        with self.assertRaisesRegex(ValueError, "exact plan revision"):
            MODULE.validate_execution(self.plan, wrong)
        wrong = copy.deepcopy(self.execution)
        wrong["frozen_policy"]["minimum_sample_per_variant"] = 100
        with self.assertRaisesRegex(ValueError, "policy differs from frozen plan"):
            MODULE.validate_execution(self.plan, wrong)
        plan = copy.deepcopy(self.plan)
        wrong = copy.deepcopy(self.execution)
        plan["frozen_policy"]["readout_end"] = "2026-10-31T10:00:00Z"
        wrong["frozen_policy"]["readout_end"] = "2026-10-31T10:00:00Z"
        with self.assertRaisesRegex(ValueError, "day-30 readout must wait"):
            MODULE.validate_execution(plan, wrong)
        wrong = copy.deepcopy(self.execution)
        wrong["source_refs"][2]["uri"] = "ticket:done"
        with self.assertRaisesRegex(ValueError, "provider launch and exposure"):
            MODULE.validate_execution(self.plan, wrong)

    def test_immature_or_underpowered_cannot_be_measured(self):
        wrong = copy.deepcopy(self.readout)
        wrong["as_of"] = "2026-10-10T11:00:00Z"
        for ref in wrong["source_refs"][1:]:
            ref["observed_at"] = "2026-10-10T09:00:00Z"
        with self.assertRaisesRegex(ValueError, "window is immature"):
            MODULE.validate_readout(self.plan, self.execution, wrong)
        wrong = copy.deepcopy(self.readout)
        wrong.update(state="measured", verdict="owner_review_required")
        with self.assertRaisesRegex(ValueError, "sample, exposure, allocation or guardrail gate"):
            MODULE.validate_readout(self.plan, self.execution, wrong)
        wrong = example("experiment-measured-readout.json")
        wrong["arms"][1]["exposed_accounts"] = 200
        with self.assertRaisesRegex(ValueError, "sample, exposure, allocation or guardrail gate"):
            MODULE.validate_readout(self.plan, self.execution, wrong)

    def test_wrong_counts_metrics_and_false_winner_are_blocked(self):
        wrong = copy.deepcopy(self.readout)
        wrong["arms"][1]["primary_rate_bps"] = 5900
        with self.assertRaisesRegex(ValueError, "arithmetic"):
            MODULE.validate_readout(self.plan, self.execution, wrong)
        wrong = copy.deepcopy(self.readout)
        wrong["primary_metric"] = "signups"
        with self.assertRaisesRegex(ValueError, "frozen metric"):
            MODULE.validate_readout(self.plan, self.execution, wrong)
        wrong = copy.deepcopy(self.readout)
        wrong["source_refs"][1]["observed_at"] = "2026-11-01T09:00:00Z"
        with self.assertRaisesRegex(ValueError, "follow the full window"):
            MODULE.validate_readout(self.plan, self.execution, wrong)
        with self.assertRaisesRegex(ValueError, "cannot claim a winner"):
            MODULE.validate_readout(self.plan, self.execution, example("invalid-experiment-readout.json"))


if __name__ == "__main__":
    unittest.main()
