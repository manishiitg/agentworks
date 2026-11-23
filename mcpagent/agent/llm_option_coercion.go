package mcpagent

// numericOption coerces a value pulled from a SavedLLM's Options map
// (json.Unmarshal lands numbers as float64) or from a config file
// hand-written by a user (might be int) into a float64. Returns
// (0, false) for anything else, including booleans and nil.
//
// We keep this in a dedicated tiny file so the option-forwarding code
// path in llm_generation.go stays a single grep target.
func numericOption(v interface{}) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	default:
		return 0, false
	}
}

// stringSliceOption coerces a value pulled from Options into a string
// slice. Accepted shapes (in the order they actually appear when a
// SavedLLM has been round-tripped through JSON, gRPC, or a YAML config):
//
//	[]string{"a", "b"}        — typed slice when the value came from Go
//	[]interface{}{"a", "b"}   — JSON-decoded slice
//	"single"                  — single-string sugar; we wrap it
//
// Anything else returns (nil, false). Empty strings inside a slice are
// dropped; an all-empty slice still returns (empty, true) so callers
// can distinguish "field present but empty" from "field absent".
func stringSliceOption(v interface{}) ([]string, bool) {
	if v == nil {
		return nil, false
	}
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, raw := range t {
			if s, ok := raw.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		if t == "" {
			return nil, false
		}
		return []string{t}, true
	default:
		return nil, false
	}
}
