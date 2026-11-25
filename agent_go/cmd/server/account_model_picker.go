package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
)

// accountModelLister is how one program lists the models a signed-in account
// may run. Cursor and Antigravity both answer per account (PLAT-872), so the
// picker asks the selected account rather than a fixed catalog.
type accountModelLister struct {
	name     string
	command  func(context.Context) (providerSetupCommand, error)
	parse    func(string) []dynamicModelEntry
	fallback func() []dynamicModelEntry
	choices  func([]dynamicModelEntry) []dynamicModelEntry
}

var accountModelListers = map[string]accountModelLister{
	"cursor-cli": {name: "Cursor", command: func(ctx context.Context) (providerSetupCommand, error) { return cursorAccountCommand(ctx, "models") },
		parse: func(out string) []dynamicModelEntry { models, _ := parseCursorModelList(out); return models }, fallback: cursorFallbackModels, choices: cursorModelChoices},
	"agy-cli": {name: "Antigravity", command: func(context.Context) (providerSetupCommand, error) {
		return providerSetupCommand{command: "agy", args: []string{"models"}}, nil
	}, parse: parseAgyModelList, fallback: agyFallbackModels, choices: func(models []dynamicModelEntry) []dynamicModelEntry { return models }},
}

var accountModelCatalog = struct {
	sync.Mutex
	models map[string][]dynamicModelEntry
}{models: make(map[string][]dynamicModelEntry)}

// The product picker discovers the selected account, never another account's
// cached inventory. Admission and allowed-model rules are the execution rules.
func (api *StreamingAPI) handleAccountModelPicker(w http.ResponseWriter, r *http.Request, provider string) {
	lister := accountModelListers[provider]
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("connection_id"))
	if id == "" {
		id = "global:" + provider
	}
	scope := providerAccountScope{Principal: caller, WorkspacePath: r.URL.Query().Get("workspace_path"), Product: r.URL.Query().Get("product")}
	if _, err := api.admitProviderAccount(r.Context(), scope, provider, id); err != nil {
		http.Error(w, "This account is unavailable here. Choose another account in Models.", http.StatusForbidden)
		return
	}
	target, err := api.resolveProviderAccountTarget(r.Context(), id, provider)
	if err != nil {
		http.Error(w, "Account could not be loaded. Reopen Models to retry.", http.StatusServiceUnavailable)
		return
	}
	if _, err = runtimeAvailableForProvider(provider); err != nil {
		http.Error(w, lister.name+" is not installed. Set it up in Providers.", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	spec, err := lister.command(ctx)
	if err != nil {
		http.Error(w, lister.name+" is still being set up. Try again shortly.", http.StatusServiceUnavailable)
		return
	}
	out, err := runProviderAccountCommand(ctx, target, spec, 20*time.Second)
	models := lister.parse(out)
	// PLAT-872-64: losing SDK authentication must not erase the account's
	// model display. Cached rows never establish readiness or execution rights.
	cacheKey := id + "\x00" + target.Home + "\x00" + spec.command + "\x00" + strings.Join(spec.args, "\x00")
	selectionDisabled := err != nil || len(models) == 0
	source, detail := "cli_account", ""
	accountModelCatalog.Lock()
	if selectionDisabled {
		models = append([]dynamicModelEntry(nil), accountModelCatalog.models[cacheKey]...)
		source, detail = "account_cache", lister.name+" models could not be refreshed. Check this account in Providers."
		if len(models) == 0 {
			models, source = lister.fallback(), "fallback_metadata"
		}
		if configured, ok := cachedProviderAccountConfigured(id); ok && configured != nil && !*configured {
			detail = lister.name + " — sign in again in Providers. Models are shown for reference."
		}
	} else {
		accountModelCatalog.models[cacheKey] = append([]dynamicModelEntry(nil), models...)
	}
	accountModelCatalog.Unlock()
	allowed, _, err := accountAllowedModels(withModelLimitPerson(r.Context(), caller), provider, id)
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
	json.NewEncoder(w).Encode(dynamicModelsResponse{Provider: provider, ModelSelectionMode: "dynamic", Models: lister.choices(filtered), Source: source, Error: detail, SelectionDisabled: selectionDisabled})
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

// parseAgyModelList reads `agy models`: one "<id>\t<name>" line per model the
// signed-in account may run, e.g. "gemini-4-neon-high\tGemini 4 Neon (High)".
// The name's parenthesis is the variant, so the picker shows one row per model
// with its levels as chips, under the company that makes it.
func parseAgyModelList(output string) []dynamicModelEntry {
	var models []dynamicModelEntry
	for _, line := range strings.Split(output, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		id, name = strings.TrimSpace(id), strings.TrimSpace(name)
		if !ok || id == "" || name == "" || strings.ContainsAny(id, " /") {
			continue
		}
		family, variant := name, ""
		if open := strings.LastIndex(name, " ("); open > 0 && strings.HasSuffix(name, ")") {
			family, variant = name[:open], name[open+2:len(name)-1]
		}
		group := cursorModelMaker(family)
		if strings.EqualFold(strings.SplitN(family, " ", 2)[0], "orchestrator") {
			group = "Google"
		}
		models = append(models, dynamicModelEntry{ModelID: id, ModelName: name, Family: family, Variant: variant, Group: group})
	}
	return models
}

// agyFallbackModels is the built-in Antigravity catalog, shown for reference
// when the account cannot list its own models.
func agyFallbackModels() []dynamicModelEntry {
	var models []dynamicModelEntry
	for _, m := range agycli.GetAllAgyCLIModels() {
		models = append(models, dynamicModelEntry{ModelID: m.ModelID, ModelName: m.ModelName, Group: "Google"})
	}
	return models
}
