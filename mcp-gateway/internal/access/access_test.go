package access

import "testing"

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
