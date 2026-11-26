package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/utils"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/mcp"
)

// mapToParameters converts a map[string]interface{} to a *llmtypes.Parameters struct.
// This helper function is used to convert normalized maps back to typed Parameters.
func mapToParameters(paramsMap map[string]interface{}) *llmtypes.Parameters {
	if paramsMap == nil {
		return nil
	}

	params := &llmtypes.Parameters{}
	if typ, ok := paramsMap["type"].(string); ok {
		params.Type = typ
	}
	// Only set properties if they exist and are not empty
	// OpenAI requires that if type is "object", properties must either be omitted or have at least one property
	if properties, ok := paramsMap["properties"].(map[string]interface{}); ok && len(properties) > 0 {
		params.Properties = properties
	}
	// Only set required if they exist and are not empty
	if required, ok := paramsMap["required"].([]interface{}); ok && len(required) > 0 {
		requiredStr := make([]string, 0, len(required))
		for _, r := range required {
			if s, ok := r.(string); ok {
				requiredStr = append(requiredStr, s)
			}
		}
		params.Required = requiredStr
	}
	if additionalProps, ok := paramsMap["additionalProperties"]; ok {
		params.AdditionalProperties = additionalProps
	}
	if patternProps, ok := paramsMap["patternProperties"].(map[string]interface{}); ok {
		params.PatternProperties = patternProps
	}
	if minProps, ok := paramsMap["minProperties"].(float64); ok {
		min := int(minProps)
		params.MinProperties = &min
	}
	if maxProps, ok := paramsMap["maxProperties"].(float64); ok {
		max := int(maxProps)
		params.MaxProperties = &max
	}
	// Store any additional fields
	params.Additional = make(map[string]interface{})
	for k, v := range paramsMap {
		switch k {
		case "type", "properties", "required", "additionalProperties", "patternProperties", "minProperties", "maxProperties":
			// Already handled
		default:
			params.Additional[k] = v
		}
	}
	return params
}

// NormalizeLLMTools normalizes array parameters in llmtypes.Tool objects to ensure
// all arrays have an 'items' field (required by Gemini and some other LLM providers).
// This function normalizes tools in-place, modifying their Parameters schema.
// Uses JSON round-trip to ensure structure preservation for llmtypes conversion.
// logger is optional - if provided, debug information will be logged at debug level
func NormalizeLLMTools(tools []llmtypes.Tool, logger loggerv2.Logger) {
	fixedCount := 0
	totalMissing := 0
	totalFixed := 0

	for i := range tools {
		if tools[i].Function != nil && tools[i].Function.Parameters != nil {
			toolName := tools[i].Function.Name

			// Handle different parameter types - convert to map for normalization
			var paramsMap map[string]interface{}
			var err error

			// Parameters is now *llmtypes.Parameters, so convert it to map for normalization
			if tools[i].Function.Parameters != nil {
				// Convert Parameters struct to map via JSON
				paramsBytes, marshalErr := json.Marshal(tools[i].Function.Parameters)
				if marshalErr != nil {
					if logger != nil {
						logger.Debug("Failed to marshal Parameters for tool",
							loggerv2.String("tool_name", toolName),
							loggerv2.Error(marshalErr))
					}
					continue
				}
				if err = json.Unmarshal(paramsBytes, &paramsMap); err != nil {
					if logger != nil {
						logger.Debug("Failed to unmarshal Parameters for tool",
							loggerv2.String("tool_name", toolName),
							loggerv2.Error(err))
					}
					continue
				}
			} else {
				if logger != nil {
					logger.Debug("Tool has nil Parameters, skipping",
						loggerv2.String("tool_name", toolName))
				}
				continue
			}

			// Debug: Check for specific problematic properties
			if props, ok := paramsMap["properties"].(map[string]interface{}); ok {
				for propName := range props {
					if propName == "assignees" || propName == "labels" || propName == "files" || propName == "reviewers" {
						prop := props[propName]
						if propMap, ok := prop.(map[string]interface{}); ok {
							propType := propMap["type"]
							hasItems := propMap["items"] != nil
							if logger != nil {
								logger.Debug("Tool property check",
									loggerv2.String("tool_name", toolName),
									loggerv2.String("property_name", propName),
									loggerv2.Any("type", propType),
									loggerv2.Any("has_items", hasItems))
							}
						}
					}
				}
			}

			beforeFix := countMissingItems(paramsMap)
			totalMissing += beforeFix
			if beforeFix > 0 {
				if logger != nil {
					logger.Debug("Tool has missing items fields",
						loggerv2.String("tool_name", toolName),
						loggerv2.Int("missing_count", beforeFix))
				}
			}
			utils.NormalizeArrayParameters(paramsMap)
			afterFix := countMissingItems(paramsMap)
			totalFixed += (beforeFix - afterFix)
			if afterFix < beforeFix {
				fixedCount++
				if logger != nil {
					logger.Debug("Fixed tool missing items",
						loggerv2.String("tool_name", toolName),
						loggerv2.Int("before", beforeFix),
						loggerv2.Int("after", afterFix))
				}
			}

			// Clean up empty properties and required arrays (OpenAI rejects empty properties)
			// Remove empty properties map - OpenAI requires properties to either be omitted or have at least one property
			if props, ok := paramsMap["properties"].(map[string]interface{}); ok && len(props) == 0 {
				delete(paramsMap, "properties")
			}
			// Remove empty required array
			if req, ok := paramsMap["required"].([]interface{}); ok && len(req) == 0 {
				delete(paramsMap, "required")
			}

			// CRITICAL: Convert normalized map back to Parameters struct
			// This ensures the structure is preserved when llmtypes processes it
			normalizedParams := mapToParameters(paramsMap)

			// Final safety check: ensure Properties and Required are nil if empty (not empty maps/arrays)
			if normalizedParams != nil {
				if normalizedParams.Properties != nil && len(normalizedParams.Properties) == 0 {
					normalizedParams.Properties = nil
				}
				if normalizedParams.Required != nil && len(normalizedParams.Required) == 0 {
					normalizedParams.Required = nil
				}
			}

			tools[i].Function.Parameters = normalizedParams
		}
	}
	if totalMissing > 0 {
		if logger != nil {
			logger.Debug("Tool normalization summary",
				loggerv2.Int("total_missing", totalMissing),
				loggerv2.Int("total_fixed", totalFixed),
				loggerv2.Int("tools_affected", fixedCount))
		}
	} else {
		if logger != nil {
			logger.Debug("All tools already have items fields")
		}
	}
}

// countMissingItems counts how many array properties are missing items field
func countMissingItems(schema map[string]interface{}) int {
	count := 0
	if schema == nil {
		return 0
	}
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		for _, propValue := range properties {
			if propMap, ok := propValue.(map[string]interface{}); ok {
				if propType, typeExists := propMap["type"].(string); typeExists && propType == "array" {
					if _, itemsExists := propMap["items"]; !itemsExists {
						count++
					}
				} else if propType == "object" {
					count += countMissingItems(propMap)
				}
			}
		}
	}
	return count
}

// ToolsAsLLM converts MCP tools to llmtypes.Tool format
func ToolsAsLLM(mcpTools []mcp.Tool) ([]llmtypes.Tool, error) {
	llmTools := make([]llmtypes.Tool, len(mcpTools))

	for i, tool := range mcpTools {
		// Convert ToolArgumentsSchema to proper JSON Schema
		schema := map[string]interface{}{
			"type": tool.InputSchema.Type,
		}

		// Only add properties if they exist and are not empty
		// OpenAI requires that if type is "object", properties must either be omitted or have at least one property
		if len(tool.InputSchema.Properties) > 0 {
			schema["properties"] = tool.InputSchema.Properties
		}
		// Don't add empty properties map - OpenAI rejects it

		// Only add required if they exist and are not empty
		if len(tool.InputSchema.Required) > 0 {
			schema["required"] = tool.InputSchema.Required
		}
		// Don't add empty required array

		// Add additional properties restriction for better validation
		schema["additionalProperties"] = false

		// Normalize array parameters to ensure all arrays have items field (required by Gemini)
		utils.NormalizeArrayParameters(schema)

		llmTools[i] = llmtypes.Tool{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  mapToParameters(schema), // Now properly formatted JSON Schema
			},
		}
	}

	return llmTools, nil
}

// ToolDetailsAsLLM converts ToolDetail structs to llmtypes.Tool format
// This is used when we have ToolDetail objects (e.g., from cache) that need to be converted to LLM tools
func ToolDetailsAsLLM(toolDetails []ToolDetail) ([]llmtypes.Tool, error) {
	llmTools := make([]llmtypes.Tool, len(toolDetails))

	for i, toolDetail := range toolDetails {
		// Convert ToolDetail to proper JSON Schema format
		schema := map[string]interface{}{
			"type": "object",
		}

		// Only add properties if they exist and are not empty
		// OpenAI requires that if type is "object", properties must either be omitted or have at least one property
		if len(toolDetail.Parameters) > 0 {
			schema["properties"] = toolDetail.Parameters
		}
		// Don't add empty properties map - OpenAI rejects it

		// Only add required if they exist and are not empty
		if len(toolDetail.Required) > 0 {
			schema["required"] = toolDetail.Required
		}
		// Don't add empty required array

		// Add additional properties restriction for better validation
		schema["additionalProperties"] = false

		// Normalize array parameters to ensure all arrays have items field (required by Gemini)
		utils.NormalizeArrayParameters(schema)

		llmTools[i] = llmtypes.Tool{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        toolDetail.Name,
				Description: toolDetail.Description,
				Parameters:  mapToParameters(schema), // Now properly formatted JSON Schema
			},
		}
	}

	return llmTools, nil
}

// ToolDetail represents detailed information about a single tool
type ToolDetail struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	Required    []string               `json:"required,omitempty"`
}

// ToolResultAsString converts a tool result to a string representation
func ToolResultAsString(result *mcp.CallToolResult) string {
	if result == nil {
		return "Tool execution completed but no result returned"
	}

	// Join all content parts
	var parts []string
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			// Try to parse JSON format {"type":"text","text":"..."}
			text := c.Text
			if strings.HasPrefix(strings.TrimSpace(text), "{") && strings.HasSuffix(strings.TrimSpace(text), "}") {
				var jsonResponse map[string]interface{}
				if err := json.Unmarshal([]byte(text), &jsonResponse); err == nil {
					// Check if it's a {"type":"text","text":"..."} format
					if responseType, ok := jsonResponse["type"].(string); ok && responseType == "text" {
						if responseText, ok := jsonResponse["text"].(string); ok {
							parts = append(parts, responseText)
							continue
						}
					}
				}
			}
			// If not JSON or not the expected format, use the text as-is
			parts = append(parts, text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[Image: %s]", c.Data))
		case *mcp.EmbeddedResource:
			parts = append(parts, fmt.Sprintf("[Resource: %s]", formatResourceContents(c.Resource)))
		default:
			// For any other content type, try to marshal to JSON
			if jsonBytes, err := json.Marshal(content); err == nil {
				parts = append(parts, string(jsonBytes))
			} else {
				parts = append(parts, fmt.Sprintf("[Unknown content type: %T]", content))
			}
		}
	}

	joined := strings.Join(parts, "\n")

	// If it's already marked as an error, return the error message
	if result.IsError {
		// If content is empty, provide a more helpful error message
		if joined == "" {
			return "Tool call failed with error: (no error details available - error result had empty content)"
		}
		return fmt.Sprintf("Tool call failed with error: %s", joined)
	}

	// Check for implicit errors in the content (even when IsError is false)
	if strings.Contains(joined, "exit status ") ||
		strings.Contains(joined, "Invalid choice") ||
		strings.Contains(joined, "Error: Access denied") {
		return fmt.Sprintf("Tool call failed with error: %s", joined)
	}

	return joined
}

// formatResourceContents formats resource contents for display
func formatResourceContents(resource mcp.ResourceContents) string {
	switch r := resource.(type) {
	case *mcp.TextResourceContents:
		return r.Text
	case *mcp.BlobResourceContents:
		return fmt.Sprintf("[Binary data: %s]", r.MIMEType)
	default:
		if jsonBytes, err := json.Marshal(resource); err == nil {
			return string(jsonBytes)
		}
		return fmt.Sprintf("[Unknown resource type: %T]", resource)
	}
}

// ParseToolArguments parses JSON string arguments into a map for MCP tool calls
func ParseToolArguments(argsJSON string) (map[string]interface{}, error) {
	if argsJSON == "" {
		return make(map[string]interface{}), nil
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}

	return args, nil
}
