package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The product picker discovers the selected account, never another account's
// cached inventory. Admission and allowed-model rules are the execution rules.
func (api *StreamingAPI) handleCursorModelPicker(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("connection_id"))
	if id == "" {
		id = "global:cursor-cli"
	}
	scope := providerAccountScope{Principal: caller, WorkspacePath: r.URL.Query().Get("workspace_path"), Product: r.URL.Query().Get("product")}
	if _, err := api.admitProviderAccount(r.Context(), scope, "cursor-cli", id); err != nil {
		http.Error(w, "This account is unavailable here. Choose another account in Models.", http.StatusForbidden)
		return
	}
	target, err := api.resolveProviderAccountTarget(r.Context(), id, "cursor-cli")
	if err != nil {
		http.Error(w, "Account could not be loaded. Reopen Models to retry.", http.StatusServiceUnavailable)
		return
	}
	_, err = runtimeAvailableForProvider("cursor-cli")
	if err != nil {
		http.Error(w, "Cursor is not installed. Set it up in Providers.", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	spec, err := cursorAccountCommand(ctx, "models")
	if err != nil {
		http.Error(w, "Cursor is still being set up. Try again shortly.", http.StatusServiceUnavailable)
		return
	}
	out, err := runProviderAccountCommand(ctx, target, spec, 15*time.Second)
	if err != nil {
		http.Error(w, "Cursor models could not be loaded. Check the account in Providers.", http.StatusBadGateway)
		return
	}
	models, _ := parseCursorModelList(string(out))
	allowed, _, err := accountAllowedModels(withModelLimitPerson(r.Context(), caller), "cursor-cli", id)
	if err != nil {
		http.Error(w, "Account model settings could not be loaded. Reopen Models to retry.", http.StatusServiceUnavailable)
		return
	}
	filtered := make([]dynamicModelEntry, 0, len(models))
	for _, m := range models {
		if len(allowed) == 0 || modelAllowed(allowed, m.ModelID) {
			filtered = append(filtered, m)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dynamicModelsResponse{Provider: "cursor-cli", ModelSelectionMode: "dynamic", Models: cursorModelChoices(filtered), Source: "cli_account"})
}

var cursorModelVersion = regexp.MustCompile(`[0-9]+`)
var cursorModelParen = regexp.MustCompile(`\s*\(([^)]*)\)`)
var cursorModelEfforts = []string{"Extra High", "Minimal", "Medium", "None", "High", "Low", "Max"}

// cursorModelChoices groups the account's Cursor list (PLAT-872). Each model came as up to fourteen
// cards ("Claude Opus 5 1M Max Thinking Fast"); split the name into its family and variant so the
// picker shows one row per family with its variants as chips, and mark families with a newer version
// as older. Every entry keeps its exact runnable ID; nothing is invented.
func cursorModelChoices(models []dynamicModelEntry) []dynamicModelEntry {
	type familyInfo struct {
		key     string
		version []int
		notes   []string
	}
	families := map[string]*familyInfo{}
	newest := map[string][]int{}
	result := make([]dynamicModelEntry, 0, len(models))
	for _, model := range models {
		name := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, model.ModelName)), " ")
		var notes []string
		for _, m := range cursorModelParen.FindAllStringSubmatch(name, -1) {
			if note := strings.TrimSpace(m[1]); note != "" && !strings.Contains(strings.ToLower(note), "default") && !strings.Contains(strings.ToLower(note), "current") {
				notes = append(notes, note)
			}
		}
		name = strings.TrimSpace(cursorModelParen.ReplaceAllString(name, ""))
		name, effort, thinking, fast, long := splitCursorModelName(name)
		parts := []string{effort}
		if effort == "" {
			parts[0] = "Standard"
		}
		if thinking != "" {
			parts = append(parts, thinking)
		}
		if fast {
			parts = append(parts, "Fast")
		}
		model.Family, model.Variant = name, strings.Join(parts, " · ")
		if strings.EqualFold(model.ModelID, "auto") {
			model.Family, model.Variant = "Auto", ""
		}
		model.Group = cursorModelMaker(model.Family)
		info := families[model.Family]
		if info == nil {
			info = &familyInfo{key: strings.Trim(cursorModelVersion.ReplaceAllString(strings.ToLower(model.Family), ""), " .-")}
			for _, part := range cursorModelVersion.FindAllString(model.Family, -1) {
				n, _ := strconv.Atoi(part)
				info.version = append(info.version, n)
			}
			families[model.Family] = info
			if versionNewer(info.version, newest[info.key]) {
				newest[info.key] = info.version
			}
		}
		if long && !slices.Contains(info.notes, "1M context") {
			info.notes = append(info.notes, "1M context")
		}
		for _, note := range notes {
			if !slices.Contains(info.notes, note) {
				info.notes = append(info.notes, note)
			}
		}
		result = append(result, model)
	}
	for i := range result {
		info := families[result[i].Family]
		result[i].FamilyNote = strings.Join(info.notes, " · ")
		result[i].Older = versionNewer(newest[info.key], info.version)
	}
	return result
}

func versionNewer(a, b []int) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// splitCursorModelName peels the variant words off the end of a Cursor name:
// "Claude Opus 5 1M Max Thinking Fast" is family "Claude Opus 5", effort Max, Thinking, Fast, 1M context.
func splitCursorModelName(name string) (family, effort, thinking string, fast, long bool) {
	cut := func(suffix string) bool {
		if len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix)-1:], " "+suffix) {
			name = strings.TrimSpace(name[:len(name)-len(suffix)-1])
			return true
		}
		return false
	}
	for {
		switch {
		case cut("Fast"):
			fast = true
		case cut("No Thinking"):
			thinking = "No thinking"
		case cut("Thinking"):
			thinking = "Thinking"
		case cut("1M"):
			long = true
		default:
			found := false
			for _, e := range cursorModelEfforts {
				if effort == "" && cut(e) {
					effort, found = e, true
					break
				}
			}
			if !found {
				return name, effort, thinking, fast, long
			}
		}
	}
}

// cursorModelMaker is the company heading a Cursor family is listed under.
func cursorModelMaker(family string) string {
	first := strings.ToLower(strings.SplitN(family, " ", 2)[0])
	switch {
	case first == "auto" || first == "composer":
		return "Cursor"
	case first == "claude":
		return "Anthropic"
	case strings.HasPrefix(first, "gpt") || first == "codex":
		return "OpenAI"
	case first == "gemini":
		return "Google"
	case first == "grok":
		return "xAI"
	case first == "glm":
		return "Z.AI"
	case first == "kimi":
		return "Moonshot"
	case first == "deepseek":
		return "DeepSeek"
	}
	return strings.SplitN(family, " ", 2)[0]
}
