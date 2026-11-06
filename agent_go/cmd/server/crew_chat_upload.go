package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
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
	// A chat attachment goes as chunks (chunk_transfer.go), after the checks above; a dashboard's small upload stays one
	// form below.
	if step := mux.Vars(r)["step"]; step != "" || strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		api.crewChunkedUpload(w, r, claims, projectID, crew.Binding.WorkspacePath, folder, mode, step)
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
	name := crewUploadName(header.Filename)
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

// crewUploadName is a safe file name from what the person's computer called it.
func crewUploadName(filename string) string {
	name := strings.Map(func(c rune) rune {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("._-", c) {
			return c
		}
		return '-'
	}, path.Base(strings.ReplaceAll(filename, "\\", "/")))
	if len(name) > 180 {
		name = "attachment" + path.Ext(name)
	}
	name = strings.Trim(name, ".-")
	if name == "" {
		name = "attachment"
	}
	return name
}

// crewUploadChunkStore is one person's chunk store for one Crew: nobody can learn from it what anyone else uploaded.
func crewUploadChunkStore(project, user string) (string, error) {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "crew-uploads", crewTransferDigest(project+"\x00"+user))
	return dir, os.MkdirAll(dir, 0o700)
}

// crewChunkedUpload is .../uploads/plan, .../uploads/chunks and the POST that finishes: {name, file}. The file is built
// from the person's chunks and goes through the same checks and writer as write_crew_file. path, from the CLI's crews
// put, names the file; without it the file goes in the caller's own attachment folder under a new name.
func (api *StreamingAPI) crewChunkedUpload(w http.ResponseWriter, r *http.Request, claims *UserClaims, project, workspace, folder, mode, step string) {
	store, err := crewUploadChunkStore(project, claims.UserID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "Uploads are unavailable right now. Attach the file again.")
		return
	}
	if step == "chunks" {
		stored, size, err := receiveChunks(r.Body, store)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "The upload was interrupted. Attach the file again.")
			return
		}
		log.Printf("[CREW_CHAT_UPLOAD] request_id=%q actor=%q crew=%q mode=%s op=send chunks=%d bytes=%d", requestLogID(r), claims.UserID, project, mode, stored, size)
		writeAgentProfileJSON(w, http.StatusOK, map[string]int{"stored": stored})
		return
	}
	var in struct {
		Name string       `json:"name"`
		Path string       `json:"path"`
		File *chunkedFile `json:"file"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&in) != nil || in.File == nil || !in.File.Valid() {
		writeAgentProfileError(w, http.StatusBadRequest, "The upload was not understood. Attach the file again.")
		return
	}
	if in.File.Size > publication.MaxFileBytes {
		writeAgentProfileError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("This file is too large. Choose a file smaller than %d MB and attach it again.", publication.MaxFileBytes>>20))
		return
	}
	if step == "plan" {
		writeAgentProfileJSON(w, http.StatusOK, map[string]any{"missing": missingChunks(store, []chunkedFile{*in.File})})
		return
	}
	started := time.Now()
	rel := in.Path
	if rel == "" {
		rel = folder + "/" + uuid.NewString() + "-" + crewUploadName(in.Name)
	}
	stage, err := os.MkdirTemp(filepath.Dir(store), "assemble-")
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "Uploads are unavailable right now. Attach the file again.")
		return
	}
	defer os.RemoveAll(stage)
	assembled := filepath.Join(stage, "file")
	err = assembleFiles(map[string]chunkedFile{"file": *in.File}, func(string) (string, error) { return assembled, nil }, func(c transferChunk) ([]byte, error) { return readChunk(store, c) })
	if err != nil {
		writeAgentProfileError(w, http.StatusConflict, "Some of the file has not arrived. Attach it again.")
		return
	}
	buffer := &externalBufferedResponse{}
	req, ok := api.externalCrewWriteRequest(buffer, r, claims, map[string]any{"crew_id": project, "path": rel}, in.File.Size)
	if ok {
		f, err := os.Open(assembled)
		if err != nil {
			writeAgentProfileError(w, http.StatusInternalServerError, "Uploads are unavailable right now. Attach the file again.")
			return
		}
		defer f.Close()
		externalPostSharedFileUpload(buffer, r, req, f)
	}
	log.Printf("[CREW_CHAT_UPLOAD] request_id=%q actor=%q crew=%q mode=%s path=%q bytes=%d status=%d", requestLogID(r), claims.UserID, project, mode, workspace+"/"+rel, in.File.Size, buffer.status)
	if buffer.status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(buffer.status)
		_, _ = w.Write(buffer.body.Bytes())
		return
	}
	pruneChunks(store, nil, crewNativeChunkKeep)
	logChunkTransfer("upload", project, "up", 1, distinctChunks([]chunkedFile{*in.File}), 0, in.File.Size, started)
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{"filepath": workspace + "/" + rel})
}
