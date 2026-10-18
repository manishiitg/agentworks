package server

// pulseImpactUpdateShape is appended to decode failures. The bare
// json.Unmarshal text names an internal Go field ("PulseGoalObservations") and
// never says that the field takes an array of objects, so a caller that passed
// a string or a bare object has nothing to correct against.
const pulseImpactUpdateShape = `interventions, observations, and assessments are each an array of objects, never a string or a bare object; ` +
	`minimal example: {"observations": [{"criterion_id": "sc-1", "metric": "posts_published", "run_id": "run-2026-08-01", "observed_at": "2026-08-01T09:00:00Z", "value": 4}], ` +
	`"interventions": [{"title": "<what changed>", "criterion_id": "sc-1", "impact_type": "reliability", "metric": "posts_published", "expected_direction": "increase"}]}`
