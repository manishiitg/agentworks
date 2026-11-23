package llmtypes

import (
	"encoding/json"
	"reflect"
	"testing"
)

// PLAT-825-142: native registration dropped action branches and line-range
// dependencies, advertising arguments the source tool would refuse.
func TestParametersPreserveSchemaRulesThroughJSON(t *testing.T) {
	const source = `{"type":"object","additionalProperties":false,"properties":{"action":{"type":"string"}},"oneOf":[{"properties":{"action":{"const":"read"}}}],"if":{"properties":{"action":{"const":"read"}}},"then":{"required":["path"]},"dependentRequired":{"end_line":["start_line"]}}`
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(source), &schema); err != nil {
		t.Fatal(err)
	}
	var decoded Parameters
	if err := json.Unmarshal([]byte(source), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Parameters{&decoded, NewParameters(schema)} {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var restored map[string]interface{}
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(restored, schema) {
			t.Fatalf("schema rules changed on a Parameters round trip: %s", data)
		}
	}
}

// Preserve server-side rules without sending root composition to model APIs.
func TestModelToolSchemaAdvertisesBranchesWithoutChangingValidation(t *testing.T) {
	var source map[string]interface{}
	if err := json.Unmarshal([]byte(`{"type":"object","oneOf":[{"properties":{"path":{"type":"string"}}},{"properties":{"path":{"type":"number"},"query":{"type":"string"}}},{"properties":{"path":{"type":"boolean"}}}],"dependentRequired":{"end_line":["start_line"]}}`), &source); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(source)
	advertised := ModelToolSchema(source)
	for _, key := range []string{"oneOf", "dependentRequired"} {
		if _, exists := advertised[key]; exists {
			t.Fatalf("model catalog retained root %s", key)
		}
	}
	fields := advertised["properties"].(map[string]interface{})
	if fields["path"] == nil || fields["query"] == nil {
		t.Fatal("model catalog lost fields from the source branches")
	}
	choices := fields["path"].(map[string]interface{})["anyOf"].([]interface{})
	if len(choices) != 3 {
		t.Fatal("differing branch fields did not form one flat union")
	}
	for _, choice := range choices {
		if choice.(map[string]interface{})["anyOf"] != nil {
			t.Fatal("advertised property union nested another union")
		}
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("advertising changed the server's validation schema")
	}
}
