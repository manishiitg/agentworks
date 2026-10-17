package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

var errPublicationChatWorking = errors.New("This chat is still working. Publish when its current reply finishes so its saved history is complete.")

type publicationRequestSessionKey struct{}

type publicationWebsiteSource struct {
	WorkspaceID     string `json:"workspace_id"`
	ConversationKey string `json:"conversation_key"`
	Destination     string `json:"destination,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	NewCrew         bool   `json:"new_crew"`
}

// Website, its chat tool and MCP all identify an exact caller-owned Local chat.
// Neither a browser path nor an arbitrary session ID is accepted as authority.
func (api *StreamingAPI) publicationWebsiteSource(r *http.Request, source publicationWebsiteSource) (ProductConversationRecord, codeLocalFileTarget, error) {
	claims := GetUserFromContext(r.Context())
	binding, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, source.WorkspaceID)
	if !ok || !binding.OwnedByCaller || manifest.Product != "code" || binding.OwnerID != sanitizeUserIDForPath(claims.UserID) {
		return ProductConversationRecord{}, codeLocalFileTarget{}, fmt.Errorf("Publish from your own Local Crew chat, or use /publish in your computer's coding chat.")
	}
	root := agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath)
	raw, exists, err := readFileFromWorkspace(r.Context(), root+"/product.json")
	var saved struct {
		Mode       string               `json:"mode"`
		LocalFiles *codeLocalFileTarget `json:"local_files"`
	}
	if err != nil || !exists || json.Unmarshal([]byte(raw), &saved) != nil || saved.LocalFiles == nil || !saved.LocalFiles.valid() {
		return ProductConversationRecord{}, codeLocalFileTarget{}, fmt.Errorf("Connect this project's computer folder in Files, then publish again.")
	}
	chats, err := defaultProductConversationRegistryStore().projectChats(r.Context(), claims.UserID, manifest.Product, manifest.ID)
	if err != nil {
		return ProductConversationRecord{}, codeLocalFileTarget{}, err
	}
	for _, chat := range chats {
		if chat.ConversationKey == source.ConversationKey && workspacePathsMatchForUser(claims.UserID, chat.WorkspacePath, binding.Binding.WorkspacePath) {
			if own, _ := r.Context().Value(publicationRequestSessionKey{}).(string); api.sessionStillWorkingAfterIdleRelease(chat.SessionID) && own != chat.SessionID {
				return ProductConversationRecord{}, codeLocalFileTarget{}, errPublicationChatWorking
			}
			return chat, *saved.LocalFiles, nil
		}
	}
	return ProductConversationRecord{}, codeLocalFileTarget{}, fmt.Errorf("Choose the Local chat you want to publish. Another person's chat cannot be imported.")
}

func (api *StreamingAPI) externalWorkspacePublish(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if err := publicationGate(claims); err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return
	}
	if externalArg(args, "action") == "publish_status" {
		upload, err := loadPublicationUpload(externalArg(args, "operation_id"), claims.UserID)
		if err != nil {
			externalError(w, 404, "not_found", "Publication unavailable. Check the receipt from your own publish.")
			return
		}
		if upload.State == "capturing" && upload.WebsiteSource != nil {
			api.startWebsitePublication(*upload, *claims)
		}
		if upload.State == "preparing" {
			api.startPublication(*upload, *claims)
		}
		externalJSON(w, publicationReceipt(upload))
		return
	}
	source := publicationWebsiteSource{WorkspaceID: externalArg(args, "source_workspace_id"), ConversationKey: externalArg(args, "conversation_key"), Destination: externalArg(args, "workspace_id"), AgentID: externalArg(args, "agent_id"), NewCrew: externalArg(args, "workspace_id") == ""}
	if _, _, err := api.publicationWebsiteSource(r, source); err != nil {
		externalError(w, 400, "source_unavailable", err.Error())
		return
	}
	projectID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(claims.UserID+"\x00"+source.WorkspaceID)).String()
	name := strings.TrimSpace(externalArg(args, "name"))
	if name == "" || len(name) > 200 {
		externalError(w, 400, "invalid_arguments", "Give the new Crew or agent a short name.")
		return
	}
	choice, err := api.publicationTarget(r.Context(), claims, externalArg(args, "profile_id"), source.Destination, source.AgentID, projectID, name)
	if source.NewCrew {
		choice, err = api.publicationChoice(r.Context(), claims, externalArg(args, "profile_id"))
	}
	if err != nil {
		externalError(w, 400, "target_unavailable", err.Error())
		return
	}
	choice.Name = name
	preview := map[string]any{"name": name, "account": map[string]any{"provider": choice.Provider, "model": choice.ModelID, "kind": "company", "use": "Company quota and billing apply; activity is attributed to the person publishing or calling this Crew."}, "continuity": "Your files and exact chat will be saved. Crew continues from a private summary; native resume comes later.", "share": map[string]any{"mode": "private"}}
	if source.Destination != "" {
		target, e := api.dashboardTarget(r.Context(), claims, choice.Root)
		if e != nil {
			externalFailure(w, e)
			return
		}
		share, e := api.readRunShare(r.Context(), claims, target)
		if e != nil {
			externalFailure(w, e)
			return
		}
		preview["share"] = share
		preview["message"] = "Adding an agent keeps this Crew's Main, instructions and Share. Its imported chat is private to Builders."
	}
	if externalArg(args, "action") == "publish_preview" {
		externalJSON(w, preview)
		return
	}
	submission := externalArg(args, "submission_id")
	if submission == "" || len(submission) > 128 {
		externalError(w, 400, "invalid_arguments", "Give this publication a stable submission ID for retries.")
		return
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(claims.UserID+"\x00"+submission)).String()
	defer lockPublication(id)()
	if prior, e := loadPublicationUpload(id, claims.UserID); e == nil {
		if prior.WebsiteSource == nil || *prior.WebsiteSource != source || prior.Name != name {
			externalError(w, 409, "submission_conflict", "Use this receipt for its original publication, or start a new submission.")
			return
		}
		if prior.State == "capturing" {
			api.startWebsitePublication(*prior, *claims)
		}
		externalJSON(w, publicationReceipt(prior))
		return
	}
	upload := publicationUpload{ID: id, ActorID: claims.UserID, SubmissionID: submission, ProjectID: projectID, Name: name, ProfileID: choice.ProfileID, WorkspaceID: choice.WorkspaceID, NewWorkspaceID: uuid.NewString(), State: "capturing", StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Record: choice, WebsiteSource: &source}
	if err := savePublicationUpload(&upload); err != nil {
		externalFailure(w, err)
		return
	}
	api.startWebsitePublication(upload, *claims)
	externalJSON(w, publicationReceipt(&upload))
}

func (api *StreamingAPI) startWebsitePublication(upload publicationUpload, claims UserClaims) {
	if _, loaded := activePublications.LoadOrStore(upload.ID, true); loaded {
		return
	}
	go func() {
		defer activePublications.Delete(upload.ID)
		ctx, cancel := context.WithTimeout(context.Background(), publication.TransferTimeout)
		defer cancel()
		ctx = context.WithValue(ctx, UserContextKey, &claims)
		fail := func(err error) {
			defer lockPublication(upload.ID)()
			upload.State = "failed"
			upload.Error = err.Error()
			upload.UpdatedAt = time.Now().UTC()
			_ = savePublicationUpload(&upload)
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/", nil)
		chat, target, err := api.publicationWebsiteSource(request, *upload.WebsiteSource)
		for errors.Is(err, errPublicationChatWorking) && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
			chat, target, err = api.publicationWebsiteSource(request, *upload.WebsiteSource)
		}
		if err != nil {
			fail(err)
			return
		}
		raw, err := ReadChatHistoryConversation(claims.UserID, chat.SessionID, chat.WorkspacePath)
		if err != nil || len(raw) == 0 {
			fail(fmt.Errorf("This chat has no saved history yet. Send a message, wait for its reply, then publish."))
			return
		}
		dir, err := publicationOperationDir(upload.ID)
		if err != nil {
			fail(err)
			return
		}
		snapshotFile := filepath.Join(dir, "files.tar")
		file, err := os.OpenFile(snapshotFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			fail(err)
			return
		}
		var offset int64
		sourceID := ""
		for {
			response, err := api.localDeviceCall(ctx, &claims, target.DeviceID, localfiles.Request{ResourceID: target.ResourceID, Operation: "publish_snapshot", Path: ".", RequestID: upload.ID, ProjectID: upload.ProjectID, Offset: offset})
			if err != nil {
				file.Close()
				fail(err)
				return
			}
			snapshot := response.Publication
			if snapshot == nil || snapshot.Offset != offset || snapshot.Size > publication.MaxBytes || snapshot.Size < 1 || offset > snapshot.Size || len(snapshot.Data) > publication.ChunkBytes || offset < snapshot.Size && len(snapshot.Data) == 0 {
				file.Close()
				fail(fmt.Errorf("The computer returned an incomplete snapshot. Reconnect and publish again."))
				return
			}
			if sourceID != "" && sourceID != snapshot.ProjectID {
				file.Close()
				fail(fmt.Errorf("The computer's project changed during publication. Publish again."))
				return
			}
			sourceID = snapshot.ProjectID
			if _, err = file.Write(snapshot.Data); err != nil {
				file.Close()
				fail(err)
				return
			}
			offset += int64(len(snapshot.Data))
			if offset == snapshot.Size {
				break
			}
		}
		if err = file.Close(); err != nil {
			fail(err)
			return
		}
		stage := filepath.Join(dir, "capture")
		_ = os.RemoveAll(stage)
		input, err := os.Open(snapshotFile)
		if err != nil {
			fail(err)
			return
		}
		snapshotManifest, unpackErr := publication.UnpackFiles(ctx, input, filepath.Join(stage, "files"), filepath.Join(stage, "history"))
		input.Close()
		if unpackErr != nil {
			fail(unpackErr)
			return
		}
		var runtimeSeeds []string
		for _, file := range snapshotManifest.Files {
			if file.Role == "runtime_seed" {
				runtimeSeeds = append(runtimeSeeds, file.Path)
			}
		}
		choice, err := api.publicationTarget(ctx, &claims, upload.ProfileID, upload.WebsiteSource.Destination, upload.WebsiteSource.AgentID, sourceID, upload.Name)
		if upload.WebsiteSource.NewCrew {
			choice, err = api.publicationChoice(ctx, &claims, upload.ProfileID)
		}
		if err != nil {
			fail(err)
			return
		}
		if choice.Provider != upload.Record.Provider || choice.ConnectionID != upload.Record.ConnectionID || choice.ModelID != upload.Record.ModelID {
			fail(fmt.Errorf("The configured company account changed. Review Publish again before uploading."))
			return
		}
		archive, _, err := publication.Build(ctx, filepath.Join(stage, "files"), dir, publication.Source{ProjectID: sourceID, RuntimeSeedPaths: runtimeSeeds, History: publication.History{Provider: "agentworks", SessionID: chat.SessionID}, Raw: raw}, nil)
		if err != nil {
			fail(err)
			return
		}
		input, err = os.Open(archive)
		if err != nil {
			fail(err)
			return
		}
		defer input.Close()
		out, err := os.OpenFile(filepath.Join(dir, "upload.tar"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			fail(err)
			return
		}
		n, err := io.Copy(out, input)
		closeErr := out.Close()
		if err != nil {
			fail(err)
			return
		}
		if closeErr != nil {
			fail(closeErr)
			return
		}
		upload.ProjectID, upload.Record, upload.WorkspaceID, upload.Offset, upload.State = sourceID, choice, choice.WorkspaceID, n, "preparing"
		if err = savePublicationUpload(&upload); err != nil {
			fail(err)
			return
		}
		api.preparePublication(&upload, claims)
		_ = os.RemoveAll(stage)
		_ = os.Remove(snapshotFile)
		_ = os.RemoveAll(filepath.Dir(archive))
	}()
}

// Imported history is private state. This helper is called by the existing
// permanent project deletion service after it has admitted the Builder.
func deletePublicationState(ctx context.Context, root string) error {
	agents, err := publishedAgents(ctx, root)
	if err != nil {
		return err
	}
	for _, p := range agents {
		history, err := p.historyDir()
		if err != nil {
			return err
		}
		if err = os.RemoveAll(history); err != nil {
			return err
		}
		file, err := publicationRecordPath(p.Root, p.AgentID)
		if err != nil {
			return err
		}
		if err = os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		if p.AgentID != "" {
			source := filepath.Dir(filepath.Dir(filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(p.sourceRoot()))))
			if err = os.RemoveAll(source); err != nil {
				return err
			}
		}
	}
	return nil
}

func (api *StreamingAPI) registerLocalPublicationTool(registrar definitionToolRegistrar, gate *productToolGate, claims *UserClaims, sessionID string, selected codeLocalFileTarget) error {
	gate.Declare("publish_project")
	return registrar.RegisterCustomToolWithTimeout("publish_project", "When the person asks to publish this Local project's files and exact chat, save them to a New Crew (Only me) or a named agent in an existing Crew (its Share and Main stay unchanged). preview discloses the company account and audience; publish queues capture after this turn finishes and continues from a private summary. Logins/.env are excluded. status reads the saved receipt. Native resume is later.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "enum": []string{"preview", "publish", "status"}}, "name": map[string]interface{}{"type": "string"}, "workspace_id": map[string]interface{}{"type": "string", "description": "Existing destination Crew from workspace list; omit for New Crew."}, "operation_id": map[string]interface{}{"type": "string"}, "submission_id": map[string]interface{}{"type": "string"}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		if !api.localWorkspaceTurnAllowed(sessionID, claims.UserID, selected) {
			return "", fmt.Errorf("Publish from the current Local chat or its website Publish button.")
		}
		api.lastQueryMu.RLock()
		turn := api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		input := map[string]any{}
		for k, v := range args {
			input[k] = v
		}
		input["action"] = map[string]string{"preview": "publish_preview", "publish": "publish", "status": "publish_status"}[externalArg(args, "action")]
		if input["action"] == "" {
			return "", fmt.Errorf("Choose preview, publish or status.")
		}
		project, _, _ := strings.Cut(turn.AgentProfileConversationKey, codeChatSideMarker)
		input["source_workspace_id"] = turn.AgentProfileID + ":" + project
		input["conversation_key"] = turn.AgentProfileConversationKey
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil).WithContext(context.WithValue(context.WithValue(ctx, UserContextKey, claims), publicationRequestSessionKey{}, sessionID))
		out := httptest.NewRecorder()
		api.externalWorkspacePublish(out, request, input)
		if out.Code >= 300 {
			return "", fmt.Errorf("%s", out.Body.String())
		}
		return out.Body.String(), nil
	}, 30*time.Second, "workspace_advanced")
}
