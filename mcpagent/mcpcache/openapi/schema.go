package openapi

import (
	"encoding/json"
	"strings"
)

// JSONSchemaToOpenAPISchema converts an MCP tool's JSON Schema parameters to an OpenAPI-compatible schema map.
// It performs minor cleanup: removes $schema if present, ensures type is "object".
// The MCP tool JSON Schema maps nearly 1:1 to OpenAPI request body schemas.
func JSONSchemaToOpenAPISchema(params interface{}) map[string]interface{} {
	if params == nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	// Marshal and unmarshal to get a clean map
	data, err := json.Marshal(params)
	if err != nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	// Remove $schema if present (not valid in OpenAPI component schemas)
	delete(schema, "$schema")

	// Ensure type is "object"
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}

	return schema
}

// ToolNameToOperationID creates a unique operation ID from server and tool names.
// Format: {server}__{tool} (double underscore separator)
// Example: google_sheets__get_document
func ToolNameToOperationID(server, tool string) string {
	return SanitizePathSegment(server) + "__" + SanitizePathSegment(tool)
}

// SanitizePathSegment converts a name to a URL-safe path segment.
// Replaces hyphens with underscores and lowercases the result.
func SanitizePathSegment(name string) string {
	// Replace hyphens with underscores for consistency
	result := strings.ReplaceAll(name, "-", "_")
	return strings.ToLower(result)
}

// GetPackageName converts server name to package name by adding _tools suffix.
// This is the canonical naming function used across the codebase.
func GetPackageName(serverName string) string {
	return sanitizeIdentifier(serverName) + "_tools"
}

// ToolNameToSnakeCase converts a tool name to snake_case for file names.
// Handles both kebab-case (resolve-library-id) and camelCase (getDocument).
func ToolNameToSnakeCase(toolName string) string {
	// First normalize: replace hyphens with underscores
	normalized := strings.ReplaceAll(toolName, "-", "_")

	// If already in snake_case, return as is
	if strings.Contains(normalized, "_") {
		return strings.ToLower(normalized)
	}

	// Convert camelCase to snake_case
	var result strings.Builder
	for i, r := range normalized {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// sanitizeIdentifier converts a string to a valid identifier (letters, digits, underscores).
func sanitizeIdentifier(name string) string {
	result := strings.Builder{}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
		if i == 0 && r >= '0' && r <= '9' {
			result.Reset()
			result.WriteString("_")
			result.WriteRune(r)
		}
	}

	identifier := result.String()
	if identifier == "" {
		identifier = "_"
	}
	return identifier
}

// SchemaNameFromToolName converts a tool name to a PascalCase schema name for OpenAPI.
// Example: "get_document" -> "GetDocumentRequest"
func SchemaNameFromToolName(toolName string) string {
	normalized := strings.ReplaceAll(toolName, "-", "_")
	parts := strings.Split(normalized, "_")
	var result strings.Builder
	for _, part := range parts {
		if len(part) > 0 {
			result.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return result.String() + "Request"
}
