package utils

// NormalizeArrayParameters recursively normalizes JSON Schema properties to ensure
// all array types have an 'items' field (required by Gemini and some other LLM providers).
// This function fixes array parameters that are missing the items field by defaulting to string type.
// It also handles nested arrays (arrays of arrays) by ensuring items.items exists when items.type == "array".
func NormalizeArrayParameters(schema map[string]interface{}) {
	if schema == nil {
		return
	}

	// Process properties if they exist
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		for _, propValue := range properties {
			if propMap, ok := propValue.(map[string]interface{}); ok {
				// Check if this property is an array type
				if propType, typeExists := propMap["type"].(string); typeExists && propType == "array" {
					// If items field is missing, add default string type
					if _, itemsExists := propMap["items"]; !itemsExists {
						propMap["items"] = map[string]interface{}{
							"type": "string",
						}
					} else {
						// If items exists, check if it's also an array (nested array)
						if itemsMap, ok := propMap["items"].(map[string]interface{}); ok {
							itemsType, ok := itemsMap["type"].(string)
							if ok && itemsType == "array" {
								// This is a nested array - ensure items.items exists
								if _, hasItemsItems := itemsMap["items"]; !hasItemsItems {
									itemsMap["items"] = map[string]interface{}{
										"type": "string",
									}
								} else {
									// Recursively normalize deeper nesting (arrays of arrays of arrays, etc.)
									if nestedItemsMap, ok := itemsMap["items"].(map[string]interface{}); ok {
										NormalizeArrayParameters(nestedItemsMap)
									}
								}
							}
							// Recursively normalize nested objects and arrays
							NormalizeArrayParameters(itemsMap)
						}
					}
				} else if propType == "object" {
					// Recursively normalize nested objects
					NormalizeArrayParameters(propMap)
				}
			}
		}
	}

	// Also handle items at the root level (for array schemas)
	if items, ok := schema["items"].(map[string]interface{}); ok {
		itemsType, ok := items["type"].(string)
		if ok && itemsType == "array" {
			// This is a nested array - ensure items.items exists
			if _, hasItemsItems := items["items"]; !hasItemsItems {
				items["items"] = map[string]interface{}{
					"type": "string",
				}
			} else {
				// Recursively normalize deeper nesting
				if nestedItemsMap, ok := items["items"].(map[string]interface{}); ok {
					NormalizeArrayParameters(nestedItemsMap)
				}
			}
		}
		// Recursively normalize nested objects and arrays
		NormalizeArrayParameters(items)
	}
}
