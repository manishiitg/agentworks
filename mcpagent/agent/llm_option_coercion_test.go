package mcpagent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestNumericOptionAcceptsJSONFloats: SavedLLM Options are persisted as
// JSON, so when the server reads back model.Options["thinking_budget"]
// the runtime type is float64 even if the user typed `2048`. The
// coercer must accept that without forcing every call site to retype.
func TestNumericOptionAcceptsJSONFloats(t *testing.T) {
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(`{"thinking_budget": 2048, "top_p": 0.9}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := numericOption(decoded["thinking_budget"])
	if !ok || got != 2048 {
		t.Errorf("thinking_budget: got (%v, %v), want (2048, true)", got, ok)
	}
	got, ok = numericOption(decoded["top_p"])
	if !ok || got != 0.9 {
		t.Errorf("top_p: got (%v, %v), want (0.9, true)", got, ok)
	}
}

// TestNumericOptionAcceptsAllNumericTypes: Go-side construction may
// supply int/int32/int64/float32 instead of float64. All must coerce
// cleanly so config-from-code and config-from-JSON behave identically.
func TestNumericOptionAcceptsAllNumericTypes(t *testing.T) {
	cases := map[string]interface{}{
		"int":     2048,
		"int32":   int32(2048),
		"int64":   int64(2048),
		"float32": float32(2048),
		"float64": float64(2048),
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := numericOption(v)
			if !ok || got != 2048 {
				t.Fatalf("got (%v, %v), want (2048, true)", got, ok)
			}
		})
	}
}

// TestNumericOptionRejectsNonNumeric: booleans, strings, nil, and slices
// must return (0, false). Otherwise we'd silently coerce e.g. true→1 and
// quietly enable thinking.
func TestNumericOptionRejectsNonNumeric(t *testing.T) {
	for _, v := range []interface{}{nil, true, "2048", []interface{}{1}, map[string]interface{}{}} {
		got, ok := numericOption(v)
		if ok || got != 0 {
			t.Errorf("non-numeric value %#v should return (0,false), got (%v,%v)", v, got, ok)
		}
	}
}

// TestStringSliceOptionHandlesJSONShape: JSON arrays decode to
// []interface{}, not []string. The forwarding path must still see a
// usable string slice.
func TestStringSliceOptionHandlesJSONShape(t *testing.T) {
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(`{"stop_sequences": ["END", "STOP", ""]}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := stringSliceOption(decoded["stop_sequences"])
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := []string{"END", "STOP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (empty strings should be filtered)", got, want)
	}
}

// TestStringSliceOptionTypedSlice: code-side construction may supply
// []string directly. Pass-through must drop empty strings the same way
// the []interface{} path does.
func TestStringSliceOptionTypedSlice(t *testing.T) {
	got, ok := stringSliceOption([]string{"END", "", "STOP"})
	if !ok || !reflect.DeepEqual(got, []string{"END", "STOP"}) {
		t.Fatalf("got (%v, %v)", got, ok)
	}
}

// TestStringSliceOptionStringSugar: a single string is convenient sugar
// for "one stop sequence". Empty string is treated as "no stop sequence".
func TestStringSliceOptionStringSugar(t *testing.T) {
	got, ok := stringSliceOption("END")
	if !ok || !reflect.DeepEqual(got, []string{"END"}) {
		t.Fatalf("string sugar: got (%v, %v)", got, ok)
	}
	if got, ok := stringSliceOption(""); ok || got != nil {
		t.Fatalf("empty string should not produce a slice; got (%v, %v)", got, ok)
	}
}

// TestStringSliceOptionRejectsNonSlice: numbers, bools, maps return
// (nil, false).
func TestStringSliceOptionRejectsNonSlice(t *testing.T) {
	for _, v := range []interface{}{nil, 1, true, map[string]interface{}{"a": "b"}} {
		got, ok := stringSliceOption(v)
		if ok || got != nil {
			t.Errorf("non-slice %#v should return (nil,false), got (%v,%v)", v, got, ok)
		}
	}
}
