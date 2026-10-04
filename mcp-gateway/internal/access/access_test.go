package access

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClonedPolicyJSONUsesEmptyArraysForUnrestrictedRules(t *testing.T) {
	p := Clone(Package{Rules: []ToolRule{{PublicName: "notion__fetch"}}})
	data, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(data), `"conditions":[]`) {
		t.Fatalf("empty conditions should serialize as an array: %s, %v", data, err)
	}
	data, err = json.Marshal(Clone(Package{}))
	if err != nil || !strings.Contains(string(data), `"rules":[]`) {
		t.Fatalf("empty policy should serialize as an array: %s, %v", data, err)
	}
}

func TestMatchIsFullStringAndMissingPathDenies(t *testing.T) {
	c := Condition{Path: "/scope/project", Op: "matches", Value: "prod-[0-9]+"}
	for _, value := range []string{"xprod-12", "prod-12x", ""} {
		if Match(c, map[string]any{"scope": map[string]any{"project": value}}) {
			t.Fatalf("partial match allowed: %q", value)
		}
	}
	if !Match(c, map[string]any{"scope": map[string]any{"project": "prod-12"}}) {
		t.Fatal("full match denied")
	}
	if Match(c, map[string]any{"scope": map[string]any{}}) {
		t.Fatal("missing path allowed")
	}
}

func TestSchemaPathMustBeExplicitString(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{"scope": map[string]any{"properties": map[string]any{"project": map[string]any{"type": "string"}}}}}
	if !SchemaHasStringPath(schema, "/scope/project") {
		t.Fatal("known path denied")
	}
	if SchemaHasStringPath(schema, "/scope/other") || SchemaHasStringPath(schema, "/scope") {
		t.Fatal("unknown or object path allowed")
	}
}

func TestPolicyRegexPreparedAndReusedWithoutStaleEdits(t *testing.T) {
	p := Clone(Package{Rules: []ToolRule{{Conditions: []Condition{{Path: "/id", Op: "matches", Value: "prod-[0-9]+"}}}}})
	c := p.Rules[0].Conditions[0]
	if c.compiled == nil {
		t.Fatal("policy clone did not prepare regex")
	}
	cloned := Clone(p)
	if cloned.Rules[0].Conditions[0].compiled != c.compiled {
		t.Fatal("runtime clone recompiled unchanged regex")
	}
	c.Value = "dev-[0-9]+"
	if Match(c, map[string]any{"id": "prod-1"}) || !Match(c, map[string]any{"id": "dev-1"}) {
		t.Fatal("edited condition used stale compiled regex")
	}
	c.Value = "["
	if Match(c, map[string]any{"id": "prod-1"}) || ValidateCondition(c) == nil {
		t.Fatal("invalid edited regex did not fail closed")
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var restored Package
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restored = Clone(restored)
	c = restored.Rules[0].Conditions[0]
	if c.compiled == nil || !Match(c, map[string]any{"id": "prod-1"}) || Match(c, map[string]any{"id": "prod-1-extra"}) {
		t.Fatal("restored regex not prepared or full-string matching changed")
	}
}
