package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// The app picker reads callable metadata, never the owner's management view
// (which includes private instructions and call history). Discovery is not a
// grant: the existing function resolver checks current target access again on
// every request, and actual invocation keeps its independent checks.
func (s *ProductScheduleService) listFunctionReferencesHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || claims.AccessToken != nil || claims.Scope != "" {
		http.Error(w, "Sign in to browse project functions.", http.StatusForbidden)
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("target"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if raw == "" || len(raw) > 4096 || len(query) > 256 {
		http.Error(w, "Choose a project to browse its functions.", http.StatusBadRequest)
		return
	}
	target, err := resolveFunctionTarget(r.Context(), claims, externalCrewCaller(claims), raw)
	if err != nil {
		// Do not echo resolver diagnostics, candidate names or private paths.
		http.Error(w, "Project functions are unavailable.", http.StatusNotFound)
		return
	}
	functions, err := callableFunctions(r.Context(), target)
	if err != nil {
		http.Error(w, "Project functions could not be loaded. Try again.", http.StatusServiceUnavailable)
		return
	}
	type reference struct {
		Name         string                 `json:"name"`
		Description  string                 `json:"description,omitempty"`
		InputSchema  map[string]interface{} `json:"input_schema,omitempty"`
		ResultSchema map[string]interface{} `json:"result_schema,omitempty"`
	}
	listed := []reference{}
	for _, fn := range functions {
		if query != "" && !strings.Contains(strings.ToLower(fn.Name+"\n"+fn.Description), query) {
			continue
		}
		listed = append(listed, reference{Name: fn.Name, Description: fn.Description, InputSchema: fn.InputSchema, ResultSchema: fn.ResultSchema})
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Name < listed[j].Name })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"functions": listed})
}
