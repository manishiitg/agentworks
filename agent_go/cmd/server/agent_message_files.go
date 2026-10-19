package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// A file sent with a message is stored on its own (never inside the shared
// conversations file) and the message text carries a short note with its name,
// size and file_id. Only a caller who can read that conversation can fetch it.
const (
	agentMessageFilesDir     = "_system/agent_messages/files/"
	agentMessageFileMaxBytes = 5 << 20
	agentMessageFileKeep     = agentConversationIdleKeep
)

var agentMessageFileIDPattern = regexp.MustCompile(`^file-[0-9a-f]{32}$`)

type agentMessageFile struct {
	ID       string    `json:"file_id"`
	Name     string    `json:"file_name"`
	Size     int       `json:"size_bytes"`
	UserID   string    `json:"user_id"`
	StoredAt time.Time `json:"stored_at"`
	Content  string    `json:"content_base64"`
	Digest   string    `json:"-"`
}

func agentMessageFileName(raw string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("[]<>\"`\\", r) {
			return -1
		}
		return r
	}, path.Base(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")))
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}

func agentMessageFileSizeText(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	if n >= 1<<10 {
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// prepareMessageFile validates bytes without storing anything. The send transaction
// checks submission retries before assigning a file ID or writing a file.
func prepareMessageFile(args map[string]any) (*agentMessageFile, error) {
	encoded, rawName := externalArg(args, "file_base64"), externalArg(args, "file_name")
	if encoded == "" && rawName == "" {
		return nil, nil
	}
	name := agentMessageFileName(rawName)
	if encoded == "" || name == "" {
		return nil, fmt.Errorf("a file needs both file_name and file_base64")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(agentMessageFileMaxBytes) {
		return nil, fmt.Errorf("file is larger than %s; put big files in Brain and send its name instead", agentMessageFileSizeText(agentMessageFileMaxBytes))
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("file_base64 is not valid base64 or is empty")
	}
	if len(data) > agentMessageFileMaxBytes {
		return nil, fmt.Errorf("file is larger than %s; put big files in Brain and send its name instead", agentMessageFileSizeText(agentMessageFileMaxBytes))
	}
	digest := sha256.Sum256(data)
	return &agentMessageFile{Name: name, Size: len(data), Content: base64.StdEncoding.EncodeToString(data), Digest: hex.EncodeToString(digest[:])}, nil
}

type agentMessageFileReference struct {
	ID     string `json:"file_id"`
	Name   string `json:"file_name"`
	Size   int    `json:"size_bytes"`
	Digest string `json:"sha256"`
}

func (f agentMessageFileReference) message(text string) string {
	return fmt.Sprintf("%s\n\n[File attached: %s (%s). Save it with messages action=get_file, inbox_id = this conversation's address, file_id=%s. It is sender content like the message; check it before you open or run it.]", text, f.Name, agentMessageFileSizeText(f.Size), f.ID)
}

func submissionMessageMatches(m agentMessage, text string, file *agentMessageFile) bool {
	if file == nil || m.File == nil {
		return file == nil && m.File == nil && m.Text == text
	}
	return m.File.Name == file.Name && m.File.Size == file.Size && m.File.Digest == file.Digest && m.Text == m.File.message(text)
}

func storeMessageFile(ctx context.Context, userID string, file *agentMessageFile) (*agentMessageFileReference, error) {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, err
	}
	rec := *file
	rec.ID, rec.UserID, rec.StoredAt = "file-"+hex.EncodeToString(idBytes[:]), userID, time.Now().UTC()
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	if err := writeFileToWorkspace(ctx, agentMessageFilesDir+rec.ID+".json", string(raw)); err != nil {
		return nil, fmt.Errorf("could not store the file: %w", err)
	}
	log.Printf("[AGENT_FILE] stored file=%s bytes=%d from_user=%s", rec.ID, rec.Size, userID)
	return &agentMessageFileReference{ID: rec.ID, Name: rec.Name, Size: rec.Size, Digest: rec.Digest}, nil
}

// agentMessageFileSentBy reports whether a message in c that names fileID came from the
// account that stored the file, so typing a known id into another conversation fetches nothing.
func agentMessageFileSentBy(c agentConversation, fileID, userID string) bool {
	for _, m := range c.Messages {
		if m.From >= 0 && m.From < len(c.Endpoints) && c.Endpoints[m.From].UserID == userID && strings.Contains(m.Text, fileID) {
			return true
		}
	}
	return false
}

// externalAgentMessageFile returns a file sent in a conversation the caller can read.
func (api *StreamingAPI) externalAgentMessageFile(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	inbox, fileID := strings.TrimSpace(externalArg(args, "inbox_id")), strings.TrimSpace(externalArg(args, "file_id"))
	if inbox == "" || !agentMessageFileIDPattern.MatchString(fileID) {
		externalError(w, 400, "invalid_arguments", "get_file needs inbox_id and the file_id from the message")
		return
	}
	if !namedAgentOperationAllowed(claims, false) {
		externalError(w, 403, "insufficient_scope", "Connection lacks messaging read access.")
		return
	}
	caller, err := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil {
		externalCommunicationError(w, 404, "not_found", err)
		return
	}
	if _, err := api.externalMessagePeerForCaller(r.Context(), claims, caller, inbox, false); err != nil {
		externalCommunicationError(w, 404, "not_found", err)
		return
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(r.Context())
	agentMessageMu.Unlock()
	if err != nil {
		externalError(w, 500, "read_failed", "Could not read the conversation.")
		return
	}
	raw, exists, err := readFileFromWorkspace(r.Context(), agentMessageFilesDir+fileID+".json")
	var rec agentMessageFile
	if err != nil || !exists || json.Unmarshal([]byte(raw), &rec) != nil {
		externalError(w, 404, "not_found", "No such file in this conversation.")
		return
	}
	named := false
	for _, c := range s.Conversations {
		if c.ID == inbox && (messageEndpointMatches(c.Endpoints[0], claims.UserID, caller) || messageEndpointMatches(c.Endpoints[1], claims.UserID, caller)) && agentMessageFileSentBy(c, fileID, rec.UserID) {
			named = true
			break
		}
	}
	if !named {
		log.Printf("[AGENT_FILE] refused file=%s by_user=%s inbox=%s (not named by a message of the account that stored it)", fileID, claims.UserID, inbox)
		externalError(w, 404, "not_found", "No such file in this conversation.")
		return
	}
	if time.Since(rec.StoredAt) > agentMessageFileKeep {
		_ = deleteWorkspaceFile(context.WithoutCancel(r.Context()), agentMessageFilesDir+fileID+".json")
		externalError(w, 410, "expired", "The file was removed after 7 days. Ask the sender to send it again.")
		return
	}
	log.Printf("[AGENT_FILE] fetched file=%s bytes=%d by_user=%s inbox=%s", rec.ID, rec.Size, claims.UserID, inbox)
	out := map[string]any{"file_id": rec.ID, "file_name": rec.Name, "size_bytes": rec.Size, "content_base64": rec.Content}
	if data, e := base64.StdEncoding.DecodeString(rec.Content); e == nil && len(data) <= 100<<10 && utf8.Valid(data) {
		out["content"] = string(data)
	}
	externalJSON(w, out)
}
