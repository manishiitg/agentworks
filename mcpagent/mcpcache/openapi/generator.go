package openapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"gopkg.in/yaml.v3"
)

// CustomToolForOpenAPI represents a custom tool for OpenAPI spec generation.
type CustomToolForOpenAPI struct {
	Definition llmtypes.Tool
	Category   string
}

// GenerateServerOpenAPISpec generates an OpenAPI 3.0 YAML spec for a single MCP server's tools.
// The spec documents per-tool POST endpoints at /tools/mcp/{server}/{tool} with request schemas
// derived from the MCP tool's JSON Schema parameters.
func GenerateServerOpenAPISpec(
	serverName string,
	tools []llmtypes.Tool,
	baseURL string,
) ([]byte, error) {
	sanitizedServer := SanitizePathSegment(serverName)

	spec := buildBaseSpec(
		fmt.Sprintf("%s Tools API", serverName),
		fmt.Sprintf("API for interacting with %s MCP server tools via HTTP", serverName),
		baseURL,
	)

	paths := make(map[string]interface{})
	schemas := make(map[string]interface{})

	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}

		toolName := tool.Function.Name
		sanitizedTool := SanitizePathSegment(toolName)
		path := fmt.Sprintf("/tools/mcp/%s/%s", sanitizedServer, sanitizedTool)
		operationID := ToolNameToOperationID(sanitizedServer, sanitizedTool)
		schemaName := SchemaNameFromToolName(toolName)

		// Convert tool parameters to OpenAPI schema
		requestSchema := JSONSchemaToOpenAPISchema(tool.Function.Parameters)
		schemas[schemaName] = requestSchema

		// Build path item
		paths[path] = map[string]interface{}{
			"post": buildOperation(
				tool.Function.Description,
				operationID,
				schemaName,
			),
		}
	}

	spec["paths"] = paths

	// Add schemas to components
	components := spec["components"].(map[string]interface{})
	components["schemas"] = schemas

	return yaml.Marshal(spec)
}

// GenerateCustomToolsOpenAPISpec generates an OpenAPI 3.0 YAML spec for custom tools in a category.
func GenerateCustomToolsOpenAPISpec(
	category string,
	tools map[string]CustomToolForOpenAPI,
	baseURL string,
) ([]byte, error) {
	spec := buildBaseSpec(
		fmt.Sprintf("%s Custom Tools API", category),
		fmt.Sprintf("API for interacting with %s custom tools via HTTP", category),
		baseURL,
	)

	paths := make(map[string]interface{})
	schemas := make(map[string]interface{})

	// Sort tool names for deterministic output
	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	for _, toolName := range toolNames {
		customTool := tools[toolName]
		if customTool.Definition.Function == nil {
			continue
		}

		sanitizedTool := SanitizePathSegment(toolName)
		path := fmt.Sprintf("/tools/custom/%s", sanitizedTool)
		operationID := ToolNameToOperationID("custom", sanitizedTool)
		schemaName := SchemaNameFromToolName(toolName)

		requestSchema := JSONSchemaToOpenAPISchema(customTool.Definition.Function.Parameters)
		schemas[schemaName] = requestSchema

		paths[path] = map[string]interface{}{
			"post": buildOperation(
				customTool.Definition.Function.Description,
				operationID,
				schemaName,
			),
		}
	}

	spec["paths"] = paths

	components := spec["components"].(map[string]interface{})
	components["schemas"] = schemas

	return yaml.Marshal(spec)
}

// buildBaseSpec creates the base OpenAPI 3.0 spec structure with security scheme.
func buildBaseSpec(title, description, baseURL string) map[string]interface{} {
	return map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":       title,
			"description": description,
			"version":     "1.0",
		},
		"servers": []map[string]interface{}{
			{"url": baseURL},
		},
		"security": []map[string]interface{}{
			{"bearerAuth": []string{}},
		},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]interface{}{
					"type":   "http",
					"scheme": "bearer",
				},
			},
			"responses": map[string]interface{}{
				"ToolResponse": map[string]interface{}{
					"description": "Tool execution result",
					"content": map[string]interface{}{
						"application/json": map[string]interface{}{
							"schema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"success": map[string]interface{}{"type": "boolean"},
									"result":  map[string]interface{}{"type": "string"},
									"error":   map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
	}
}

// GenerateCompactSpec generates a minimal, token-efficient spec for MCP server tools.
// Format is a simple text listing of endpoints with inlined parameter types,
// roughly 70-80% fewer tokens than OpenAPI YAML.
func GenerateCompactSpec(serverName string, tools []llmtypes.Tool, baseURL string) string {
	sanitized := SanitizePathSegment(serverName)
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("base: %s\n", baseURL))
	sb.WriteString("auth: Bearer $MCP_API_TOKEN\n\n")

	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		// The exact name, so an agent sees one spelling in search_tools and here.
		path := fmt.Sprintf("/tools/mcp/%s/%s", sanitized, tool.Function.Name)
		writeCompactEntry(&sb, "POST", path, tool.Function.Description, tool.Function.Parameters)
	}

	return sb.String()
}

// GenerateCustomToolsCompactSpec generates a minimal spec for custom tools.
func GenerateCustomToolsCompactSpec(category string, tools map[string]CustomToolForOpenAPI, baseURL string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("base: %s\n", baseURL))
	sb.WriteString("auth: Bearer $MCP_API_TOKEN\n\n")

	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	for _, toolName := range toolNames {
		ct := tools[toolName]
		if ct.Definition.Function == nil {
			continue
		}
		path := fmt.Sprintf("/tools/custom/%s", SanitizePathSegment(toolName))
		writeCompactEntry(&sb, "POST", path, ct.Definition.Function.Description, ct.Definition.Function.Parameters)
	}

	return sb.String()
}

// GenerateVirtualToolsCompactSpec documents runtime tools that support the
// session-scoped virtual HTTP executor. Callers supply only admitted tools.
func GenerateVirtualToolsCompactSpec(tools []llmtypes.Tool, baseURL string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("base: %s\n", baseURL))
	sb.WriteString("auth: Bearer $MCP_API_TOKEN\n\n")
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		endpoint := "/tools/virtual/" + SanitizePathSegment(tool.Function.Name)
		writeCompactEntry(&sb, "POST", endpoint, tool.Function.Description, tool.Function.Parameters)
	}
	return sb.String()
}

// writeCompactEntry writes a single endpoint block in compact format.
// The tool description is stripped of markdown and rendered as indented comment lines
// so the LLM has enough context to choose between tools.
func writeCompactEntry(sb *strings.Builder, method, path, description string, params interface{}) {
	sb.WriteString(method + " " + path + "\n")
	if description != "" {
		lines := formatDescription(description, 100)
		for _, line := range lines {
			sb.WriteString("  # " + line + "\n")
		}
	}
	writeCompactParams(sb, params)
	sb.WriteString("\n")
}

// formatDescription returns the description split into lines, preserving the original
// markdown formatting. No stripping, no truncation — the LLM reads markdown natively
// and needs the full content (## sections, bullet tips, examples) to use tools correctly.
func formatDescription(desc string, _ int) []string {
	raw := strings.Split(strings.TrimRight(desc, "\n"), "\n")
	// Filter out completely empty trailing lines but keep internal blank lines for structure
	for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}

// writeCompactParams writes flattened parameter lines: "  name: type (required) # desc"
func writeCompactParams(sb *strings.Builder, params interface{}) {
	if params == nil {
		return
	}
	data, err := json.Marshal(params)
	if err != nil {
		return
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return
	}

	props, _ := schema["properties"].(map[string]interface{})
	if len(props) == 0 {
		return
	}

	required := make(map[string]bool)
	if req, ok := schema["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		prop, _ := props[name].(map[string]interface{})
		typeStr := compactType(prop)
		suffix := ""
		if required[name] {
			suffix = " (required)"
		}
		// Include short description if present
		if desc, _ := prop["description"].(string); desc != "" {
			// Only take the first line of the description — no length truncation so
			// the LLM gets the full context (e.g. allowed values, format examples).
			if idx := strings.Index(desc, "\n"); idx > 0 {
				desc = desc[:idx]
			}
			sb.WriteString(fmt.Sprintf("  %s: %s%s  # %s\n", name, typeStr, suffix, desc))
		} else {
			sb.WriteString(fmt.Sprintf("  %s: %s%s\n", name, typeStr, suffix))
		}
	}
}

// compactType returns a short type string for a JSON Schema property.
func compactType(prop map[string]interface{}) string {
	if prop == nil {
		return "any"
	}
	t, _ := prop["type"].(string)
	switch t {
	case "array":
		if items, ok := prop["items"].(map[string]interface{}); ok {
			itemType, _ := items["type"].(string)
			if itemType != "" {
				return "array[" + itemType + "]"
			}
		}
		return "array"
	case "object":
		// Show nested property names if available
		if nested, ok := prop["properties"].(map[string]interface{}); ok && len(nested) > 0 {
			keys := make([]string, 0, len(nested))
			for k := range nested {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > 4 {
				keys = append(keys[:4], "...")
			}
			return "object{" + strings.Join(keys, ",") + "}"
		}
		return "object"
	case "":
		// anyOf / oneOf — just say "any"
		return "any"
	default:
		// Check for enum values
		if enums, ok := prop["enum"].([]interface{}); ok && len(enums) > 0 && len(enums) <= 6 {
			parts := make([]string, 0, len(enums))
			for _, e := range enums {
				parts = append(parts, fmt.Sprintf("%v", e))
			}
			return t + "(" + strings.Join(parts, "|") + ")"
		}
		return t
	}
}

// buildOperation creates an OpenAPI path operation for a tool endpoint.
func buildOperation(description, operationID, schemaName string) map[string]interface{} {
	op := map[string]interface{}{
		"operationId": operationID,
		"requestBody": map[string]interface{}{
			"required": true,
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{
					"schema": map[string]interface{}{
						"$ref": fmt.Sprintf("#/components/schemas/%s", schemaName),
					},
				},
			},
		},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{
				"$ref": "#/components/responses/ToolResponse",
			},
		},
	}

	// Only add summary if description is non-empty; keep it short
	if description != "" {
		// Truncate long descriptions for the summary field
		summary := description
		if idx := strings.Index(summary, "\n"); idx > 0 {
			summary = summary[:idx]
		}
		if len(summary) > 120 {
			summary = summary[:117] + "..."
		}
		op["summary"] = summary
	}

	return op
}
