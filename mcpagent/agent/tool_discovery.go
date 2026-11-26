package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func searchToolsDefinition() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "search_tools",
		Description: "Discover currently authorized platform and connected-app tools by intent, name, group, or server. Returns exact names and short descriptions, not schemas. Use get_api_spec with a returned name before calling an HTTP tool. If a query misses, omit query and enumerate a group/server, paging with next_offset. Discovery never grants permission or executes anything.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"query":       map[string]interface{}{"type": "string", "description": "Intent or tool-name keywords; omit to enumerate."},
				"group":       map[string]interface{}{"type": "string", "description": "Optional exact custom-tool display group from a previous result."},
				"server_name": map[string]interface{}{"type": "string", "description": "Optional exact connected MCP server from a previous result."},
				"offset":      map[string]interface{}{"type": "integer", "minimum": 0},
				"limit":       map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 20, "default": 10, "description": "Page size; a page is at least 10 tools."},
			},
		}),
	}}
}

type discoveredTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       string `json:"group,omitempty"`
	Server      string `json:"server_name,omitempty"`
	score       int
}

// Search uses this Agent's registry and the same turn/server authorization as
// schema lookup. Never query codeexec's process-wide union of session tools.
func (a *Agent) handleSearchTools(ctx context.Context, args map[string]interface{}) (string, error) {
	values := map[string]string{}
	for _, key := range []string{"query", "group", "server_name"} {
		if raw, exists := args[key]; exists {
			value, ok := raw.(string)
			if !ok || len(value) > 512 {
				return "", fmt.Errorf("%s must be a string of at most 512 bytes", key)
			}
			values[key] = strings.TrimSpace(value)
		}
	}
	offset, err := discoveryInteger(args, "offset", 0, 0, 1_000_000)
	if err != nil {
		return "", err
	}
	limit, err := discoveryInteger(args, "limit", 10, 1, 1_000_000)
	if err != nil {
		return "", err
	}
	// A page of one or two tools made weaker models page through a group call by call (28 calls in one turn), so a
	// smaller request is raised to the default page rather than refused. A larger one (agents ask for 30) gets the
	// largest page instead of an error that costs them a turn.
	limit = min(max(limit, discoveryMinPageSize), 20)
	registry, err := a.canonicalRegistry()
	if err != nil {
		return "", err
	}
	terms := strings.FieldsFunc(strings.ToLower(values["query"]), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	results := make([]discoveredTool, 0)
	groups, servers := map[string]bool{}, map[string]bool{}
	for _, tool := range registry.snapshot() {
		if tool.Kind == toolImplementationVirtual || !a.isToolAllowedForContext(ctx, tool.Name) || !codeexec.IsSessionToolAllowed(a.sessionID, tool.Name) {
			continue
		}
		server := ""
		if tool.Kind == toolImplementationMCP {
			if !a.serverIsAvailable(tool.Source) {
				continue
			}
			server = tool.agentServerName()
			servers[server] = true
		} else if tool.DisplayGroup != "" {
			groups[tool.DisplayGroup] = true
		}
		if values["group"] != "" && (tool.Kind != toolImplementationDirect || tool.DisplayGroup != values["group"]) {
			continue
		}
		if values["server_name"] != "" && normalizeServerKey(server) != normalizeServerKey(values["server_name"]) {
			continue
		}
		description := ""
		if tool.Definition.Function != nil {
			description = tool.Definition.Function.Description
		}
		name := strings.ToLower(tool.Name)
		text := strings.ToLower(description + " " + tool.DisplayGroup + " " + server)
		score := 0
		for _, term := range terms {
			if strings.Contains(name, term) {
				score += 4
			} else if strings.Contains(text, term) {
				score++
			}
		}
		if len(terms) > 0 && score == 0 {
			continue
		}
		if query := strings.ToLower(values["query"]); name == query || strings.ToLower(tool.realName()) == query {
			score += 100
		}
		runes := []rune(strings.Join(strings.Fields(description), " "))
		if len(runes) > 350 {
			runes = append(runes[:350], '…')
		}
		results = append(results, discoveredTool{Name: tool.Name, Description: string(runes), Group: tool.DisplayGroup, Server: server, score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].Name < results[j].Name
	})
	total := len(results)
	start := min(offset, total)
	end := min(start+limit, total)
	var next *int
	if end < total {
		next = &end
	}
	status := "ok"
	if total == 0 {
		status = "no_matches"
	}
	data, err := json.Marshal(struct {
		Status  string           `json:"status"`
		Tools   []discoveredTool `json:"tools"`
		Total   int              `json:"total"`
		Next    *int             `json:"next_offset,omitempty"`
		Groups  []string         `json:"groups"`
		Servers []string         `json:"servers"`
	}{status, results[start:end], total, next, discoveryKeys(groups), discoveryKeys(servers)})
	return string(data), err
}

func discoveryKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// discoveryMinPageSize is the smallest page search_tools returns.
const discoveryMinPageSize = 10

func discoveryInteger(args map[string]interface{}, name string, fallback, low, high int) (int, error) {
	raw, exists := args[name]
	if !exists {
		return fallback, nil
	}
	var value float64
	switch v := raw.(type) {
	case int:
		value = float64(v)
	case float64:
		value = v
	case json.Number:
		var err error
		value, err = v.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value < float64(low) || value > float64(high) {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, low, high)
	}
	return int(value), nil
}
