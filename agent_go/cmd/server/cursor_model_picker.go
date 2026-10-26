package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
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
	bin, err := runtimeAvailableForProvider("cursor-cli")
	if err != nil {
		http.Error(w, "Cursor is not installed. Set it up in Providers.", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--list-models")
	cmd.Env = target.Env
	if !target.Server {
		// This ambient alternative token must not override a private account.
		cmd.Env = nil
		for _, entry := range target.Env {
			if !strings.HasPrefix(entry, "CURSOR_AUTH_TOKEN=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
	}
	out, err := cmd.Output()
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
	json.NewEncoder(w).Encode(dynamicModelsResponse{Provider: "cursor-cli", ModelSelectionMode: "dynamic", Models: latestCursorModelFamilies(filtered), Source: "cli_account"})
}

var cursorModelVersion = regexp.MustCompile(`[0-9]+`)
var cursorModelVariant = regexp.MustCompile(`(?i)(?:[ -]+(?:extra[ -]+high|xhigh|high|medium|low|fast|thinking|default|current|\(default\)|\(current\)))+$`)
var cursorModelAnnotation = regexp.MustCompile(`\s*\([^)]*(?:default|current)[^)]*\)\s*$`)

// PLAT-872: the exploded CLI list produced a card per speed/effort variant,
// alongside older generations. Keep one actual selector per newest family;
// never manufacture an ID or infer access from the static metadata catalog.
func latestCursorModelFamilies(models []dynamicModelEntry) []dynamicModelEntry {
	type choice struct {
		model   dynamicModelEntry
		version []int
		rank    int
	}
	picks := map[string]choice{}
	order := []string{}
	for _, model := range models {
		name := strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, model.ModelName)
		name = strings.Join(strings.Fields(cursorModelAnnotation.ReplaceAllString(name, "")), " ")
		label := strings.TrimSpace(cursorModelVariant.ReplaceAllString(name, ""))
		family := strings.Trim(cursorModelVersion.ReplaceAllString(strings.ToLower(label), ""), " .-")
		var version []int
		for _, part := range cursorModelVersion.FindAllString(label, -1) {
			n, _ := strconv.Atoi(part)
			version = append(version, n)
		}
		rank := 0
		if label == name {
			rank = 2
		} else if strings.HasSuffix(strings.ToLower(name), " medium") {
			rank = 1
		}
		if model.IsDefault {
			rank = 3
		}
		previous, exists := picks[family]
		newer := !exists
		equal := exists
		if exists {
			for i := 0; i < len(version) || i < len(previous.version); i++ {
				a, b := 0, 0
				if i < len(version) {
					a = version[i]
				}
				if i < len(previous.version) {
					b = previous.version[i]
				}
				if a != b {
					newer = a > b
					equal = false
					break
				}
			}
		}
		if !exists {
			order = append(order, family)
		}
		if newer || (equal && rank > previous.rank) {
			model.ModelName = label
			picks[family] = choice{model, version, rank}
		}
	}
	result := make([]dynamicModelEntry, 0, len(order))
	for _, family := range order {
		result = append(result, picks[family].model)
	}
	return result
}
