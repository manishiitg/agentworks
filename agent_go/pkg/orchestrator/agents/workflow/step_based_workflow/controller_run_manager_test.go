package step_based_workflow

import (
	"encoding/json"
	"testing"
)

func TestPlanRevisionForFilesIsCanonicalAndChangesWithContract(t *testing.T) {
	leftPlan, err := canonicalJSONDocument(`{"steps":[{"id":"one","title":"First"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	rightPlan, err := canonicalJSONDocument(`{ "steps": [ { "title": "First", "id": "one" } ] }`)
	if err != nil {
		t.Fatal(err)
	}
	leftID, _, err := planRevisionForFiles(map[string]interface{}{"planning/plan.json": leftPlan})
	if err != nil {
		t.Fatal(err)
	}
	rightID, payload, err := planRevisionForFiles(map[string]interface{}{"planning/plan.json": rightPlan})
	if err != nil {
		t.Fatal(err)
	}
	if leftID != rightID {
		t.Fatalf("semantically identical JSON produced revisions %q and %q", leftID, rightID)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("revision payload is invalid JSON: %v", err)
	}

	changed, _ := canonicalJSONDocument(`{"steps":[{"id":"one","title":"Changed"}]}`)
	changedID, _, err := planRevisionForFiles(map[string]interface{}{"planning/plan.json": changed})
	if err != nil {
		t.Fatal(err)
	}
	if changedID == leftID {
		t.Fatal("behavioral plan change did not change the revision")
	}
}
