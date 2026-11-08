package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

// Use the editor's source authority and the website's existing validated input
// writer. Preset-only changes preserve the current declared input definitions.
func (api *StreamingAPI) externalSaveScriptInputs(w http.ResponseWriter, r *http.Request, item DiscoveredWorkflow, args map[string]any) {
	forward := r.Clone(r.Context())
	forward.Method = http.MethodPut
	forward.URL.Path = "/api/variable-groups"
	forward.URL.RawQuery = "workspace_path=" + url.QueryEscape(item.WorkspacePath)
	// Normalize authority refusals as well as input-validation errors. A native
	// http.Error must not escape as plain text in the external JSON protocol.
	externalBuilderForward(w, forward, func(w http.ResponseWriter, request *http.Request) {
		if api.crewScriptAuthoringBinding(w, request) == nil {
			return
		}
		definition, err := readCrewScript(request.Context(), item.WorkspacePath)
		if err != nil {
			externalFailure(w, err)
			return
		}
		inputs := any(definition.Inputs)
		if value, present := args["inputs"]; present {
			inputs = value
		}
		body, err := json.Marshal(map[string]any{"variables": inputs, "groups": args["input_presets"]})
		if err != nil {
			externalFailure(w, err)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.Header.Set("Content-Type", "application/json")
		api.handleUpdateVariableGroups(w, request)
	})
}
