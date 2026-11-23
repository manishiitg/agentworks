package llmtypes

import "encoding/json"

// Parameters carries JSON Schema through native tool registration. Keep rules
// outside its typed fields, such as oneOf and dependentRequired, on round trips.
func (p Parameters) MarshalJSON() ([]byte, error) {
	type fields Parameters
	data, err := json.Marshal(fields(p))
	if err != nil {
		return nil, err
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	for key, value := range p.Additional {
		if parameterJSONField(key) {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		schema[key] = encoded
	}
	return json.Marshal(schema)
}

func (p *Parameters) UnmarshalJSON(data []byte) error {
	type fields Parameters
	var typed fields
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return err
	}
	for key := range schema {
		if parameterJSONField(key) {
			delete(schema, key)
		}
	}
	*p = Parameters(typed)
	p.Additional = schema
	return nil
}

func parameterJSONField(key string) bool {
	switch key {
	case "type", "properties", "required", "additionalProperties", "patternProperties", "minProperties", "maxProperties":
		return true
	}
	return false
}
