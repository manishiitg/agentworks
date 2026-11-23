package llmtypes

import "reflect"

// ModelToolSchema advertises the union of an object's fields without root
// composition rules unsupported by model tool catalogs. The original schema
// remains authoritative for execution and is never changed here.
func ModelToolSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}
	out := make(map[string]interface{}, len(schema))
	for key, value := range schema {
		out[key] = value
	}
	properties := map[string]interface{}{}
	root, _ := schema["properties"].(map[string]interface{})
	for key, value := range root {
		properties[key] = value
	}
	var collect func(map[string]interface{})
	collect = func(branch map[string]interface{}) {
		fields, _ := branch["properties"].(map[string]interface{})
		for key, value := range fields {
			if _, authoritative := root[key]; authoritative {
				continue
			}
			if prior, found := properties[key]; found && !reflect.DeepEqual(prior, value) {
				choices := []interface{}{prior}
				if union, ok := prior.(map[string]interface{}); ok && len(union) == 1 {
					if existing, ok := union["anyOf"].([]interface{}); ok {
						choices = append([]interface{}{}, existing...)
					}
				}
				properties[key] = map[string]interface{}{"anyOf": append(choices, value)}
			} else {
				properties[key] = value
			}
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			branches, _ := branch[key].([]interface{})
			for _, raw := range branches {
				if child, ok := raw.(map[string]interface{}); ok {
					collect(child)
				}
			}
		}
		for _, key := range []string{"then", "else"} {
			if child, ok := branch[key].(map[string]interface{}); ok {
				collect(child)
			}
		}
	}
	collect(schema)
	if len(properties) > 0 {
		out["properties"] = properties
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "if", "then", "else", "not", "dependentRequired", "dependentSchemas"} {
		delete(out, key)
	}
	return out
}
