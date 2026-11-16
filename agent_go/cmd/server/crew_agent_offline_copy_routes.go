package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// Upload authority is the source's current attached connection, not the person
// merely knowing a Team/agent/session ID. This does not grant history reads.
func crewOfflineSource(ctx context.Context, t crewTeam, agent string, b crewOfflineBinding, name string) error {
	if err := crewOfflineTerminalOwner(ctx, t, agent, b, name); err != nil {
		return err
	}
	wanted, err := crewOfflineAppWanted(t, t.Agents[agent], b)
	if err != nil || !wanted {
		return fmt.Errorf("source copy not requested")
	}
	return nil
}

func crewOfflineTerminalOwner(ctx context.Context, t crewTeam, agent string, b crewOfflineBinding, name string) error {
	claims := GetUserFromContext(ctx)
	if claims == nil || !crewTeamAccountActive(claims.UserID) {
		return fmt.Errorf("source membership unavailable")
	}
	row, ok := t.Agents[agent]
	if !ok || crewOfflineSourceOwner(t, claims.UserID, row) != nil || row.Terminal == nil || row.ChatKey != b.Session || row.Terminal.Provider != b.Provider {
		return fmt.Errorf("source binding unavailable")
	}
	if name == "" && claims.AccessToken != nil {
		name = claims.AccessToken.BoundAgentName
	}
	named, _, err := currentAgentAttachment(ctx, claims, name, b.Attachment)
	if err != nil || named.ID != row.Terminal.NamedAgentID || named.Attachment == nil || named.Attachment.Provider != b.Provider || named.Attachment.SessionID != b.Session {
		return fmt.Errorf("source attachment unavailable")
	}
	current, err := currentCrewTeamTerminalWithPair(ctx, t, row, func() (string, bool) { return crewOfflineMakerPair(t, row) })
	if err != nil {
		return err
	}
	if current.Attachment == nil || current.Attachment.ID != b.Attachment {
		return fmt.Errorf("source attachment changed")
	}
	return nil
}

func (api *StreamingAPI) externalOfflineCopyStatus(w http.ResponseWriter, r *http.Request, args map[string]any) {
	b := crewOfflineBinding{Provider: externalArg(args, "provider"), Session: externalArg(args, "session"), Attachment: externalArg(args, "attachment_id")}
	name := externalArg(args, "agent_name")
	claims := GetUserFromContext(r.Context())
	if name == "" && claims != nil && claims.AccessToken != nil {
		name = claims.AccessToken.BoundAgentName
	}
	named, _, err := currentAgentAttachment(r.Context(), claims, name, b.Attachment)
	if err != nil || named.Attachment == nil || named.Attachment.Provider != b.Provider || named.Attachment.SessionID != b.Session {
		externalError(w, 403, "attachment_unavailable", "This exact source attachment is unavailable.")
		return
	}
	t, a, joined, err := terminalCrewForNamed(named.ID)
	if err != nil {
		externalError(w, 503, "copy_unavailable", "Copy status is unavailable.")
		return
	}
	if !joined || crewOfflineSource(r.Context(), t, a.ID, b, name) != nil {
		externalJSON(w, map[string]bool{"shared": false})
		return
	}
	dir, err := crewTeamDir(t.ProjectID)
	if err != nil {
		externalError(w, 503, "copy_unavailable", "Copy status is unavailable.")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	current, err := readCrewTeam(t.ProjectID)
	if err == nil {
		err = crewOfflineSource(r.Context(), current, a.ID, b, name)
	}
	if err != nil {
		externalError(w, 403, "source_changed", "The source sharing or attachment changed.")
		return
	}
	dir, err = crewOfflineDirectory(t.ProjectID, a.ID)
	var h crewOfflineHistory
	if err == nil {
		h, err = readCrewOfflineHistory(dir, b)
	}
	if err != nil {
		externalError(w, 409, "copy_unavailable", "The source history binding changed.")
		return
	}
	if current.OnlineHome == "" {
		api.queueCrewOfflineNativeHome(r.Context(), current, current.Agents[a.ID])
	}
	externalJSON(w, map[string]any{"shared": true, "project": t.ProjectID, "agent": a.ID, "revision": h.Revision, "offset": h.Offset, "tail_hash": h.TailHash})
}

func (api *StreamingAPI) handleCrewOfflineCopy(w http.ResponseWriter, r *http.Request) {
	project, agent, op := mux.Vars(r)["project"], mux.Vars(r)["agent"], mux.Vars(r)["op"]
	fail := func(status int, reason string) {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=refused reason=%s", project, agent, reason)
		externalError(w, status, "offline_copy_refused", "The source session, sharing or history cursor changed; refresh its copy status.")
	}
	var in crewOfflineDelta
	name := r.URL.Query().Get("agent_name")
	if op == "chunks" {
		if r.Method != "PUT" {
			fail(405, "method")
			return
		}
		in.crewOfflineBinding = crewOfflineBinding{Provider: r.URL.Query().Get("provider"), Session: r.URL.Query().Get("session"), Attachment: r.URL.Query().Get("attachment")}
	} else {
		if r.Method != "POST" {
			fail(405, "method")
			return
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in) != nil {
			fail(400, "invalid_manifest")
			return
		}
	}
	if in.Provider != "agentworks" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) {
		fail(403, "attachment_required")
		return
	}
	if !in.crewOfflineBinding.valid() {
		fail(400, "invalid_binding")
		return
	}
	teamDir, err := crewTeamDir(project)
	if err != nil {
		fail(403, "source_unavailable")
		return
	}
	mu := crewTeamRecordMutex(teamDir)
	mu.Lock()
	defer mu.Unlock()
	t, err := readCrewTeam(project)
	if err != nil || crewOfflineRequestSource(r, t, agent, in.crewOfflineBinding, name) != nil {
		fail(403, "source_unavailable")
		return
	}
	dir, err := crewOfflineDirectory(project, agent)
	if err != nil {
		fail(503, "store_unavailable")
		return
	}
	h, err := readCrewOfflineHistory(dir, in.crewOfflineBinding)
	if err != nil {
		fail(409, "binding_changed")
		return
	}
	store := filepath.Join(dir, "chunks")
	switch op {
	case "status":
		externalJSON(w, h.crewOfflineCursor)
	case "plan":
		if !crewOfflineDeltaReplayed(h, in) {
			if err = validateCrewOfflineDelta(h, in); err != nil {
				fail(409, "cursor_changed")
				return
			}
		}
		externalJSON(w, map[string]any{"missing": missingChunks(store, []chunkedFile{in.Delta})})
	case "chunks":
		limited := &io.LimitedReader{R: r.Body, N: chunkBatchBytes + 1}
		frames, payload := 0, 0
		err = readChunkFrames(limited, func(id string, data []byte) error { frames++; payload += len(data); return putChunk(store, id, data) })
		if err != nil {
			// Parser errors contain counts, never frame contents. Strip OS paths
			// from storage errors before redaction; no source text belongs in logs.
			detail := err.Error()
			var pathErr *os.PathError
			var linkErr *os.LinkError
			if errors.As(err, &pathErr) {
				detail = pathErr.Op + ": " + pathErr.Err.Error()
			}
			if errors.As(err, &linkErr) {
				detail = linkErr.Op + ": " + linkErr.Err.Error()
			}
			log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=chunks_failed frames=%d payload_bytes=%d stream_bytes=%d content_length=%d limit=%d reason=%q", project, agent, frames, payload, chunkBatchBytes+1-limited.N, r.ContentLength, chunkBatchBytes, redaction.Text(detail))
			fail(400, "invalid_chunks")
			return
		}
		if crewOfflineRequestSource(r, t, agent, in.crewOfflineBinding, name) != nil {
			fail(403, "source_changed")
			return
		}
		externalJSON(w, map[string]bool{"stored": true})
	case "commit":
		if crewOfflineRequestSource(r, t, agent, in.crewOfflineBinding, name) != nil {
			fail(403, "source_changed")
			return
		}
		ack, e := commitCrewOfflineDelta(project, agent, dir, h, in)
		if e != nil {
			fail(409, "delta_refused")
			return
		}
		// Build or refresh the home copy in the background, outside the source lock; the upload never waits on it.
		go api.refreshCrewOfflineCopy(project, agent)
		externalJSON(w, ack)
	default:
		fail(400, "invalid_operation")
	}
}

// The CLI child credential gets only this exact ingest surface, not general
// Crew API access. No prefix grant, transcript GET or arbitrary method.
func crewOfflineCopyOAuthPath(method, path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 8 || parts[0] != "" || parts[1] != "api" || parts[2] != "crew-teams" || !teamOnlineIdentifier.MatchString(parts[3]) || parts[4] != "agents" || parts[6] != "offline-copy" {
		return false
	}
	id := strings.TrimPrefix(parts[5], crewTeamAgentIDPrefix)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || !strings.HasPrefix(parts[5], crewTeamAgentIDPrefix) {
		return false
	}
	return method == http.MethodPut && parts[7] == "chunks" || method == http.MethodPost && (parts[7] == "status" || parts[7] == "plan" || parts[7] == "commit" || parts[7] == "handback" || parts[7] == "handback-ack")
}
