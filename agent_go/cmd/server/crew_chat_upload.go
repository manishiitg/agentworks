package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// Both Builder and Run chat attachments use the existing Crew file writer.
// The browser supplies a file, never the destination or the caller's role.
func (api *StreamingAPI) handleCrewChatUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		writeAgentProfileError(w, http.StatusUnauthorized, "Sign in, then attach the file again.")
		return
	}
	if userAccessForClaims(claims).Disabled {
		writeAgentProfileError(w, http.StatusForbidden, "This account is disabled. Ask an administrator to enable it, then attach the file again.")
		return
	}
	projectID := strings.TrimSpace(mux.Vars(r)["project_id"])
	if mux.Vars(r)["id"] != "work" {
		writeAgentProfileError(w, http.StatusNotFound, "Open an available Crew, then attach the file again.")
		return
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, projectID)
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "This Crew is unavailable. Ask its owner to share it with you, then attach the file again.")
		return
	}
	if token := claims.AccessToken; token != nil && !token.Allows("crews:run") && !token.Allows("crews:write") {
		writeAgentProfileError(w, http.StatusForbidden, "Allow this connection to use the Crew, then attach the file again.")
		return
	}
	folder, mode, limit := "uploads", "Builder", int64(10<<20)
	if !crew.OwnedByCaller {
		folder, mode, limit = "shared/"+sanitizeUserIDForPath(claims.UserID), "Run", crewSharedFileMaxBytes
	} else if status, _ := externalCrewAuthorGate(claims); status != 0 {
		writeAgentProfileError(w, status, "This account cannot add Crew files. Ask an administrator to allow file uploads.")
		return
	}
	// Include room for multipart headers without reading an unbounded file.
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		status, reason := http.StatusBadRequest, "multipart_invalid"
		message := "The upload was not understood. Attach the file again."
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
			status, reason = http.StatusRequestEntityTooLarge, "multipart_too_large"
			message = fmt.Sprintf("This file is too large. Choose a file smaller than %d MB and attach it again.", limit>>20)
		}
		log.Printf("[CREW_CHAT_UPLOAD] request_id=%q actor=%q owner=%q crew=%q mode=%s status=%d reason=%s content_type=%q content_length=%d error=%q", requestLogID(r), claims.UserID, crew.OwnerID, projectID, mode, status, reason, r.Header.Get("Content-Type"), r.ContentLength, err.Error())
		writeAgentProfileError(w, status, message)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "Choose a file to attach, then try again.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "The file could not be read. Attach it again.")
		return
	}
	if int64(len(data)) > limit {
		log.Printf("[CREW_CHAT_UPLOAD] request_id=%q actor=%q owner=%q crew=%q mode=%s status=413 reason=file_too_large limit=%d", requestLogID(r), claims.UserID, crew.OwnerID, projectID, mode, limit)
		writeAgentProfileError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("This file is too large. Choose a file smaller than %d MB and attach it again.", limit>>20))
		return
	}
	name := strings.Map(func(c rune) rune {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("._-", c) {
			return c
		}
		return '-'
	}, path.Base(strings.ReplaceAll(header.Filename, "\\", "/")))
	if len(name) > 180 {
		name = "attachment" + path.Ext(name)
	}
	name = strings.Trim(name, ".-")
	if name == "" {
		name = "attachment"
	}
	rel := folder + "/" + uuid.NewString() + "-" + name
	buffer := &externalBufferedResponse{}
	api.externalWriteCrewFile(buffer, r, claims, map[string]any{
		"crew_id": projectID, "path": rel, "content_base64": base64.StdEncoding.EncodeToString(data),
	})
	log.Printf("[CREW_CHAT_UPLOAD] request_id=%q actor=%q owner=%q crew=%q mode=%s path=%q bytes=%d status=%d", requestLogID(r), claims.UserID, crew.OwnerID, projectID, mode, crew.Binding.WorkspacePath+"/"+rel, len(data), buffer.status)
	if buffer.status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(buffer.status)
		_, _ = w.Write(buffer.body.Bytes())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{"filepath": crew.Binding.WorkspacePath + "/" + rel})
}
