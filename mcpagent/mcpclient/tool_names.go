package mcpclient

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Model-facing MCP tool names are "<alias>__<tool>". They are worked out from
// the app and the tool alone, never from a session, a credential or a
// connection's internal name, so one tool has one name in every chat and the
// server can recompute it from a live inventory without remembering anything.
const (
	ToolNameSeparator  = "__"
	MaxToolNameLength  = 64 // Anthropic and OpenAI both cap tool names at 64
	MaxToolAliasLength = 24
	minToolAliasLength = 8
	toolClashTailChars = 6
)

var (
	invalidToolNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	repeatedUnderscores  = regexp.MustCompile(`_{2,}`)
)

// CleanToolNamePart keeps [a-zA-Z0-9_-] and never leaves "__" inside, so the
// first "__" of a visible name is always the alias separator.
func CleanToolNamePart(value string) string {
	value = invalidToolNameChars.ReplaceAllString(value, "_")
	value = repeatedUnderscores.ReplaceAllString(value, "_")
	return strings.Trim(value, "_")
}

// NormalizeToolName is the one comparison form for tool and server names:
// hyphens, underscores and case do not distinguish a name an agent typed.
func NormalizeToolName(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
}

// ToolAlias is the short name of an app for MCPServerConfig.ToolAlias. It uses
// underscores only, so a URL path segment spells it the same way.
func ToolAlias(app string) string {
	alias := CleanToolNamePart(NormalizeToolName(app))
	if len(alias) > MaxToolAliasLength {
		alias = strings.TrimRight(alias[:MaxToolAliasLength], "_-")
	}
	if alias == "" {
		return "mcp"
	}
	return alias
}

// toolWord is a tool's own name. Namespaced servers (a gateway) prefix every
// tool with "<namespace>__", which says nothing the alias does not.
func toolWord(tool string, namespaced bool) string {
	if namespaced {
		if _, word, ok := strings.Cut(tool, ToolNameSeparator); ok && word != "" {
			return word
		}
	}
	return tool
}

// fitToolName joins alias and word. Over the limit it keeps the tool word and
// shortens the alias; only a word too long for even a short alias loses its
// middle, keeping both ends so similar tools still read apart.
func fitToolName(alias, word string) string {
	room := MaxToolNameLength - len(ToolNameSeparator)
	if len(alias)+len(word) <= room {
		return alias + ToolNameSeparator + word
	}
	keep := max(room-len(word), min(len(alias), minToolAliasLength))
	alias = strings.TrimRight(alias[:min(keep, len(alias))], "_-")
	if budget := room - len(alias); len(word) > budget {
		head := budget * 2 / 3
		tail := budget - head - 1
		word = strings.TrimRight(word[:head], "_-") + "_" + strings.TrimLeft(word[len(word)-tail:], "_-")
	}
	return alias + ToolNameSeparator + word
}

// ToolNames gives every real tool of one server its model-facing name, valid
// for every provider. Only real tools that would otherwise share a name get a
// short tail, taken from the real tool name, so it is the same in every session.
func ToolNames(alias string, tools []string, namespaced bool) map[string]string {
	names := make(map[string]string, len(tools))
	owners := make(map[string][]string, len(tools))
	for _, tool := range tools {
		if _, done := names[tool]; done {
			continue
		}
		word := CleanToolNamePart(toolWord(tool, namespaced))
		if word == "" {
			word = "tool"
		}
		name := fitToolName(alias, word)
		names[tool] = name
		owners[name] = append(owners[name], tool)
	}
	for name, clashing := range owners {
		if len(clashing) == 1 {
			continue
		}
		for _, tool := range clashing {
			sum := sha256.Sum256([]byte(tool))
			tail := "_" + hex.EncodeToString(sum[:])[:toolClashTailChars]
			names[tool] = strings.TrimRight(name[:min(len(name), MaxToolNameLength-len(tail))], "_-") + tail
		}
	}
	return names
}

// MatchToolName finds the real tool an agent means. An exact real or visible
// name wins; otherwise the real name, the visible name and the bare tool word
// are compared in normalized form, so hyphens, underscores and case cannot be
// wrong. Different tools that normalize alike are not guessed between: real is
// empty and options lists their exact names. No match returns both empty.
func MatchToolName(asked, alias string, tools []string, namespaced bool) (real string, options []string) {
	names := ToolNames(alias, tools, namespaced)
	for _, tool := range tools {
		if tool == asked || names[tool] == asked {
			return tool, nil
		}
	}
	key := NormalizeToolName(asked)
	var matches []string
	for _, tool := range tools {
		if key == NormalizeToolName(tool) || key == NormalizeToolName(names[tool]) || key == NormalizeToolName(toolWord(tool, namespaced)) {
			matches = append(matches, tool)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	for _, tool := range matches {
		options = append(options, names[tool])
	}
	sort.Strings(options)
	return "", options
}
