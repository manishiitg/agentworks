package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Dashboard uploads are the same Crew attachments as chat uploads. Destination,
// caller attribution, size limits and write checks remain in one handler.
func (api *StreamingAPI) dashboardUploadTarget(w http.ResponseWriter, r *http.Request, root string) (dashboardTarget, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.Scope != "" || userAccessForClaims(claims).Disabled {
		externalError(w, 403, "forbidden", "Sign in to the Crew to add a reference file.")
		return dashboardTarget{}, false
	}
	target, err := api.dashboardTarget(r.Context(), claims, root)
	if err != nil {
		dashboardError(w, err)
		return target, false
	}
	if target.Kind != "crew" {
		externalError(w, 400, "invalid_target", "Reference uploads are available in Crew dashboards.")
		return target, false
	}
	return target, true
}

func (api *StreamingAPI) forwardDashboardUpload(w http.ResponseWriter, r *http.Request, target dashboardTarget) {
	request := mux.SetURLVars(r, map[string]string{"id": "work", "project_id": target.ID})
	response := &externalBufferedResponse{}
	api.handleCrewChatUpload(response, request)
	if response.status != http.StatusOK {
		for name, values := range response.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(response.status)
		_, _ = w.Write(response.body.Bytes())
		return
	}
	var result struct {
		Filepath string `json:"filepath"`
	}
	if json.Unmarshal(response.body.Bytes(), &result) != nil || !strings.HasPrefix(result.Filepath, target.Root+"/") {
		externalError(w, 502, "upload_failed", "The file was uploaded but its saved path could not be read.")
		return
	}
	publishReportChanged(target.Root)
	externalJSON(w, map[string]any{"path": strings.TrimPrefix(result.Filepath, target.Root+"/"), "filepath": result.Filepath})
}

func (api *StreamingAPI) handleDashboardUpload(w http.ResponseWriter, r *http.Request) {
	target, ok := api.dashboardUploadTarget(w, r, r.URL.Query().Get("workspace"))
	if ok {
		api.forwardDashboardUpload(w, r, target)
	}
}

func (api *StreamingAPI) dashboardUploadBase64(w http.ResponseWriter, r *http.Request, root, name, encoded string) {
	target, ok := api.dashboardUploadTarget(w, r, root)
	if !ok {
		return
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		externalError(w, 400, "invalid_upload", "Send the file's base64 content.")
		return
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", name)
	if err != nil {
		externalError(w, 400, "invalid_upload", "Choose a file name.")
		return
	}
	if _, err = file.Write(data); err != nil {
		externalError(w, 400, "invalid_upload", "The file could not be read.")
		return
	}
	_ = writer.Close()
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(&body)
	clone.ContentLength = int64(body.Len())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Content-Type", writer.FormDataContentType())
	api.forwardDashboardUpload(w, clone, target)
}
