package server

import (
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// externalMCPToolSearch is the discovery tool of the remote MCP surface, the same idea as the search_tools
// tool inside the platform's own agents: find tools by intent or name without listing the whole catalog.
const externalMCPToolSearch = "search_tools"

const (
	externalSearchDefaultLimit = 10
	externalSearchMaxLimit     = 20
)

// externalMCPSearchTools searches the connection's listed tools by keywords in the name and one-line
// description. It returns names and descriptions only (schemas come from get_api_spec) and never widens
// what the connection may use. A page is at least ten tools so a model does not walk a group call by call.
func externalMCPSearchTools(args map[string]any, listed []externalTool) *mcp.CallToolResult {
	query, _ := args["query"].(string)
	if len(query) > 512 {
		return mcp.NewToolResultError("invalid_arguments: query is at most 512 characters")
	}
	offset, ok := externalSearchInt(args, "offset", 0)
	if !ok || offset < 0 {
		return mcp.NewToolResultError("invalid_arguments: offset must be an integer of 0 or more")
	}
	limit, ok := externalSearchInt(args, "limit", externalSearchDefaultLimit)
	if !ok || limit < 1 || limit > externalSearchMaxLimit {
		return mcp.NewToolResultError("invalid_arguments: limit must be an integer between 1 and 20 (a page is at least 10)")
	}
	limit = max(limit, externalSearchDefaultLimit)

	words := strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(query)))
	type hit struct {
		tool  externalTool
		score int
	}
	hits := make([]hit, 0, len(listed))
	for _, tool := range listed {
		name := strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(tool.Name))
		description := strings.ToLower(tool.Description)
		score, matched := 0, true
		for _, word := range words {
			switch {
			case strings.Contains(name, word):
				score += 2
			case strings.Contains(description, word):
				score++
			default:
				matched = false
			}
		}
		if matched {
			hits = append(hits, hit{tool, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].tool.Name < hits[j].tool.Name
	})
	end := min(offset+limit, len(hits))
	start := min(offset, end)
	entries := make([]map[string]string, 0, end-start)
	for _, h := range hits[start:end] {
		entries = append(entries, map[string]string{"name": h.tool.Name, "description": h.tool.Description})
	}
	out := map[string]any{"tools": entries, "total": len(hits), "note": "Names and short descriptions only. Pass names to get_api_spec for schemas, then run them with call_tool. Discovery grants no permission."}
	if end < len(hits) {
		out["next_offset"] = end
	}
	result, err := mcp.NewToolResultJSON(out)
	if err != nil {
		return mcp.NewToolResultError("failed to encode search result: " + err.Error())
	}
	return result
}

func externalSearchInt(args map[string]any, name string, fallback int) (int, bool) {
	raw, exists := args[name]
	if !exists || raw == nil {
		return fallback, true
	}
	value, ok := raw.(float64)
	if !ok || value != float64(int(value)) {
		return 0, false
	}
	return int(value), true
}
