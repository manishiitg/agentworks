package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type publicationUpload struct {
	Audience       *runShareState    `json:"audience,omitempty"`
	ID             string            `json:"operation_id"`
	ActorID        string            `json:"actor_id"`
	SubmissionID   string            `json:"submission_id"`
	ProjectID      string            `json:"project_id"`
	Name           string            `json:"name"`
	ProfileID      string            `json:"profile_id"`
	WorkspaceID    string            `json:"workspace_id,omitempty"`
	NewWorkspaceID string            `json:"new_workspace_id,omitempty"`
	State          string            `json:"state"`
	Offset         int64             `json:"offset"`
	StartedAt      time.Time         `json:"started_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Record         *publishedProject `json:"record,omitempty"`
	Excluded       []string          `json:"excluded,omitempty"`
	FileCount      int               `json:"file_count,omitempty"`
	RawBytes       int64             `json:"raw_bytes,omitempty"`
	TabError       string            `json:"tab_error,omitempty"`
	RawSHA256      string            `json:"raw_sha256,omitempty"`
	Error          string            `json:"error,omitempty"`
}

var publicationLocks sync.Map
var activePublications sync.Map
var publicationCancels sync.Map
var publicationStoreLocks [64]sync.Mutex

func lockPublication(id string) func() {
	value, _ := publicationLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func publicationOperationDir(id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("invalid publication receipt")
	}
	root, err := publicationStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "uploads", id), nil
}

func savePublicationUpload(upload *publicationUpload) error {
	var bucket uint32
	for _, ch := range upload.ID {
		bucket = bucket*31 + uint32(ch)
	}
	mu := &publicationStoreLocks[bucket%uint32(len(publicationStoreLocks))]
	mu.Lock()
	defer mu.Unlock()
	if upload.State != "deleted" {
		if prior, e := loadPublicationUpload(upload.ID, upload.ActorID); e == nil && prior.State == "deleted" {
			return fmt.Errorf("This Crew was deleted; the old publication will not be replayed.")
		}
	}

	dir, err := publicationOperationDir(upload.ID)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "operation.json"), upload, 0600)
}

func loadPublicationUpload(id, actor string) (*publicationUpload, error) {
	dir, err := publicationOperationDir(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "operation.json"))
	if err != nil {
		return nil, err
	}
	var upload publicationUpload
	if json.Unmarshal(b, &upload) != nil || upload.ID != id || upload.ActorID != actor {
		return nil, fmt.Errorf("publication unavailable")
	}
	return &upload, nil
}

func publicationReceipt(upload *publicationUpload) map[string]any {
	out := map[string]any{"limits": map[string]any{"bytes": publication.MaxBytes, "file_bytes": publication.MaxFileBytes, "files": publication.MaxFiles, "chunk_bytes": publication.ChunkBytes}, "operation_id": upload.ID, "state": upload.State, "uploaded_bytes": upload.Offset, "updated_at": upload.UpdatedAt, "native_resume": "not_attempted", "next": "Check workspace action=publish_status with this operation_id."}
	if upload.Audience != nil {
		out["share"] = upload.Audience
	}
	if upload.Record != nil {
		out["workspace_id"], out["revision"] = upload.Record.ProfileID+":"+upload.Record.WorkspaceID, upload.Record.Revision
		out["agent_id"] = firstNonEmptyTrimmed(upload.Record.AgentID, "main")
		out["conversation_key"] = upload.Record.conversationKey()
		out["name"] = upload.Record.Name
		if upload.TabError != "" {
			out["tab_message"] = upload.TabError
		}
		out["account"] = map[string]any{"provider": upload.Record.Provider, "model": upload.Record.ModelID, "kind": "company", "use": "Company account quota and billing apply; activity is attributed to the person publishing or calling this Crew."}
	}
	if upload.State == "live" {
		out["file_count"], out["excluded"], out["excluded_count"] = upload.FileCount, upload.Excluded, len(upload.Excluded)
		out["history"] = map[string]any{"raw_saved": true, "raw_bytes": upload.RawBytes, "raw_sha256": upload.RawSHA256, "summary_ready": true, "audience": "builders"}
		out["message"] = "Your files and chat are saved. This Crew works while your computer is offline and continues from a private summary. To change its source, publish again from your computer."
		out["next"] = "Open the Crew or use Share to choose who can use it."
	}
	if upload.Error != "" {
		out["error"] = upload.Error
	}
	return out
}

func publicationGate(claims *UserClaims) error {
	if !externalWorkspaceAllowed(claims) || !userAccessForClaims(claims).CanEdit {
		return fmt.Errorf("Publishing needs your existing Builder access. Ask an administrator to enable building, or use a Crew already shared with you.")
	}
	return nil
}

func (api *StreamingAPI) publicationChoice(ctx context.Context, claims *UserClaims, profileID string, existing ...bool) (*publishedProject, error) {
	if profileID == "" && workspaceOriginAllowed(claims, crewProfileID) && userAccessForClaims(claims).CanCreateIn(crewProfileID) {
		profileID = crewProfileID
	}
	if !isProjectProfileID(profileID) || !workspaceOriginAllowed(claims, profileID) || (len(existing) == 0 || !existing[0]) && (!userAccessForClaims(claims).CanCreateIn(profileID) || !workspaceCreateTokenAllowed(claims)) {
		return nil, fmt.Errorf("This connection cannot create that Crew. Choose a product you may build in.")
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil {
		return nil, err
	}
	profile = profileWithProductDefault(ctx, profile)
	provider, model := resolveProfileRuntimeModel(profile.Runtime, "", "")
	if provider == "" || model == "" {
		return nil, fmt.Errorf("Choose a configured company coding account in Providers before publishing.")
	}
	connection := serverDefaultConnectionID(provider)
	if _, err = api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, Product: profileID}, provider, connection); err != nil {
		return nil, err
	}
	return &publishedProject{ProfileID: profileID, OwnerID: sanitizeUserIDForPath(claims.UserID), Provider: provider, ModelID: model, ConnectionID: connection}, nil
}

// Native CLI uploads use the existing sign-in; no session minting or laptop credential transfer.
func (api *StreamingAPI) handlePublicationUpload(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if err := publicationGate(claims); err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return
	}
	id := mux.Vars(r)["operation"]
	if id == "" {
		var in struct {
			SubmissionID string `json:"submission_id"`
			ProjectID    string `json:"project_id"`
			Name         string `json:"name"`
			ProfileID    string `json:"profile_id"`
			WorkspaceID  string `json:"workspace_id"`
			AgentID      string `json:"agent_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil || len(in.SubmissionID) < 1 || len(in.SubmissionID) > 128 || len(in.ProjectID) < 1 || len(in.ProjectID) > 128 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
			externalError(w, 400, "invalid_arguments", "Give a project name, stable project identity and submission ID.")
			return
		}
		operation := uuid.NewSHA1(uuid.NameSpaceOID, []byte(claims.UserID+"\x00"+in.SubmissionID)).String()
		defer lockPublication(operation)()
		if prior, e := loadPublicationUpload(operation, claims.UserID); e == nil {
			if prior.ProjectID != in.ProjectID || prior.Name != in.Name {
				externalError(w, 409, "submission_conflict", "This submission already belongs to another publication.")
				return
			}
			externalJSON(w, publicationReceipt(prior))
			return
		}
		choice, e := api.publicationTarget(r.Context(), claims, in.ProfileID, in.WorkspaceID, in.AgentID, in.ProjectID, in.Name)
		if e != nil {
			externalError(w, 400, "target_unavailable", e.Error())
			return
		}

		upload := &publicationUpload{ID: operation, ActorID: claims.UserID, SubmissionID: in.SubmissionID, ProjectID: in.ProjectID, Name: strings.TrimSpace(in.Name), ProfileID: choice.ProfileID, WorkspaceID: choice.WorkspaceID, NewWorkspaceID: uuid.NewString(), State: "uploading", StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Record: choice}
		if choice.Root != "" {
			if target, e := api.dashboardTarget(r.Context(), claims, choice.Root); e == nil {
				if audience, e := api.readRunShare(r.Context(), claims, target); e == nil {
					upload.Audience = &audience
				}
			}
		} else {
			upload.Audience = &runShareState{Mode: runSharePrivate}
		}
		if err := savePublicationUpload(upload); err != nil {
			externalError(w, 500, "storage_error", "The publication receipt could not be saved; retry.")
			return
		}
		log.Printf("[CREW_PUBLISH] operation=%s actor=%s state=uploading", operation, sanitizeUserIDForPath(claims.UserID))
		externalJSON(w, publicationReceipt(upload))
		return
	}
	defer lockPublication(id)()
	upload, err := loadPublicationUpload(id, claims.UserID)
	if err != nil {
		externalError(w, 404, "not_found", "Publication unavailable. Start publishing from your own project again.")
		return
	}
	if r.Method == http.MethodGet {

		if upload.State == "preparing" {
			api.startPublication(*upload, *claims)
		}
		externalJSON(w, publicationReceipt(upload))
		return
	}
	if upload.State == "live" || upload.State == "preparing" || upload.State == "deleted" {
		externalJSON(w, publicationReceipt(upload))
		return
	}
	if upload.Record == nil || !workspaceOriginAllowed(claims, upload.ProfileID) || !publicationOriginTokenAllowed(claims, upload.ProfileID, upload.WorkspaceID) {
		externalError(w, 403, "forbidden", "This connection no longer permits publishing to that Crew.")
		return
	}
	if upload.WorkspaceID != "" {
		binding, _, ok := api.resolveExternalWorkspace(r.Context(), claims, upload.ProfileID+":"+upload.WorkspaceID)
		if !ok || !binding.OwnedByCaller {
			externalError(w, 403, "forbidden", "You no longer have Builder access to this Crew.")
			return
		}
	}
	dir, _ := publicationOperationDir(id)
	if r.Method == http.MethodPut {
		var in struct {
			Offset int64  `json:"offset"`
			Data   []byte `json:"data"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || len(in.Data) == 0 || len(in.Data) > publication.ChunkBytes || in.Offset < 0 || in.Offset+int64(len(in.Data)) > publication.MaxBytes {
			externalError(w, 413, "transfer_limit", "A publication chunk exceeds the server's transfer limit.")
			return
		}
		if time.Since(upload.StartedAt) > publication.TransferTimeout || time.Since(upload.UpdatedAt) > publication.IdleTimeout {
			upload.StartedAt = time.Now().UTC()
			upload.State = "uploading" // retry resumes the same bytes/receipt
		}
		f, e := os.OpenFile(filepath.Join(dir, "upload.tar"), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			externalError(w, 500, "storage_error", "Upload storage is unavailable; retry this publication.")
			return
		}
		defer f.Close()
		if in.Offset < upload.Offset {
			saved := make([]byte, len(in.Data))
			_, e = f.ReadAt(saved, in.Offset)
			if e != nil || !bytes.Equal(saved, in.Data) {
				externalError(w, 409, "offset_conflict", "This retry does not match saved upload bytes. Start a new publication.")
				return
			}
		} else if in.Offset == upload.Offset {
			if _, e = f.WriteAt(in.Data, in.Offset); e != nil || f.Sync() != nil {
				externalError(w, 500, "storage_error", "This upload chunk could not be saved; retry.")
				return
			}
			upload.Offset += int64(len(in.Data))
		} else {
			externalError(w, 409, "offset_conflict", "Resume from the uploaded_bytes offset in your receipt.")
			return
		}
		upload.UpdatedAt = time.Now().UTC()
		upload.Error = ""
		if e = savePublicationUpload(upload); e != nil {
			externalError(w, 500, "storage_error", "The upload receipt could not be saved; retry.")
			return
		}
		externalJSON(w, publicationReceipt(upload))
		return
	}
	if upload.Offset == 0 {
		externalError(w, 400, "incomplete", "Upload the complete project package before publishing.")
		return
	}
	upload.State = "preparing"
	upload.Error = ""
	upload.UpdatedAt = time.Now().UTC()
	if err = savePublicationUpload(upload); err != nil {
		externalError(w, 500, "storage_error", "Publication could not start; retry.")
		return
	}
	// The server owns this operation; the browser only displays the saved receipt.
	api.startPublication(*upload, *claims)
	externalJSON(w, publicationReceipt(upload))
}

func (api *StreamingAPI) findPublishedProject(ctx context.Context, claims *UserClaims, projectID string) (*publishedProject, error) {
	root, err := publicationStateRoot()
	if err != nil {
		return nil, err
	}
	var found *publishedProject
	paths, _ := filepath.Glob(filepath.Join(root, "projects", "*.json"))
	for _, file := range paths {
		raw, e := os.ReadFile(file)
		if e != nil {
			continue
		}
		var rec publishedProject
		if json.Unmarshal(raw, &rec) != nil || firstNonEmptyTrimmed(rec.PublisherID, rec.OwnerID) != sanitizeUserIDForPath(claims.UserID) || rec.ProjectID != projectID {
			continue
		}
		binding, _, ok := api.resolveExternalWorkspace(ctx, claims, rec.ProfileID+":"+rec.WorkspaceID)
		if ok && binding.OwnedByCaller && canonicalCrewWorkspaceRoot(binding.Binding.WorkspacePath) == canonicalCrewWorkspaceRoot(rec.Root) {
			if found != nil {
				return nil, fmt.Errorf("This project was published to several Crews. Choose its destination with --crew or workspace_id.")
			}
			copy := rec
			found = &copy
		}
	}
	return found, nil
}

func (api *StreamingAPI) startPublication(upload publicationUpload, claims UserClaims) {
	if _, loaded := activePublications.LoadOrStore(upload.ID, true); loaded {
		return
	}
	go func() { defer activePublications.Delete(upload.ID); api.preparePublication(&upload, claims) }()
}

func (api *StreamingAPI) preparePublication(upload *publicationUpload, claims UserClaims) {
	ctx, cancel := context.WithTimeout(context.Background(), publication.TransferTimeout)
	publicationCancels.Store(upload.ID, cancel)
	defer publicationCancels.Delete(upload.ID)
	defer cancel()
	current, err := accessTokenClaims(accesstokens.Token{UserID: claims.UserID, Username: claims.Username, Email: claims.Email, Provider: claims.Provider})
	if err == nil {
		current.AccessToken = claims.AccessToken
		claims = *current
	}
	ctx = productpolicy.WithSelection(context.WithValue(ctx, UserContextKey, &claims), builderProductSelection(&claims))
	finishError := func(err error) {
		defer lockPublication(upload.ID)()
		upload.State = "failed"
		upload.Error = err.Error()
		upload.UpdatedAt = time.Now().UTC()
		_ = savePublicationUpload(upload)
		log.Printf("[CREW_PUBLISH] operation=%s actor=%s state=failed", upload.ID, sanitizeUserIDForPath(upload.ActorID))
	}
	if err != nil || publicationGate(&claims) != nil {
		finishError(fmt.Errorf("Publishing access changed. Sign in again and retry."))
		return
	}
	dir, err := publicationOperationDir(upload.ID)
	if err != nil {
		finishError(err)
		return
	}
	stage := filepath.Join(dir, "prepared")
	_ = os.RemoveAll(stage) // this operation's temporary import only; active revisions/history are elsewhere
	input, err := os.Open(filepath.Join(dir, "upload.tar"))
	if err != nil {
		finishError(err)
		return
	}
	manifest, err := publication.Unpack(ctx, input, filepath.Join(stage, "source"), filepath.Join(stage, "history"))
	_ = input.Close()
	if err != nil {
		finishError(fmt.Errorf("Project package is incomplete or invalid: %w. Your previous version is still live.", err))
		return
	}
	if manifest.ProjectID != upload.ProjectID {
		finishError(fmt.Errorf("The project identity does not match this publication receipt."))
		return
	}
	// Serialise target activation across computers while keeping admitted calls on immutable versions.
	defer lockPublication("project:" + upload.Record.OwnerID + ":" + manifest.ProjectID)()
	record := *upload.Record
	if latest, e := loadPublicationUpload(upload.ID, upload.ActorID); e == nil && latest.State == "deleted" {
		return
	}
	if upload.WorkspaceID == "" {
		// The receipt persisted this ID before creating anything, so retry after a restart finds one target.
		id := upload.NewWorkspaceID
		binding, _, exists := api.resolveExternalWorkspace(ctx, &claims, upload.ProfileID+":"+id)
		if !exists {
			id, err = api.createWorkspaceFromSpec(ctx, &claims, upload.ProfileID, crewSpec{Name: upload.Name}, false, upload.NewWorkspaceID)
			if err != nil {
				finishError(err)
				return
			}
			binding, _, exists = api.resolveExternalWorkspace(ctx, &claims, upload.ProfileID+":"+id)
		}
		if !exists || !binding.OwnedByCaller {
			finishError(fmt.Errorf("The new Crew could not be opened. Retry this publication."))
			return
		}
		record.WorkspaceID = id
		record.Root = agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath)
		record.OwnerID = binding.OwnerID
		record.PublisherID = sanitizeUserIDForPath(upload.ActorID)
		record.Name = upload.Name
	} else {
		binding, _, ok := api.resolveExternalWorkspace(ctx, &claims, record.ProfileID+":"+record.WorkspaceID)
		if !ok || !binding.OwnedByCaller {
			finishError(fmt.Errorf("You can no longer publish to this Crew."))
			return
		}
	}
	defer lockPublication("target:" + canonicalCrewWorkspaceRoot(record.Root))()
	if latest, e := loadPublicationUpload(upload.ID, upload.ActorID); e == nil && latest.State == "deleted" {
		return
	}
	record.PublisherID = sanitizeUserIDForPath(upload.ActorID)
	record.Name = upload.Name
	record.ProjectID = manifest.ProjectID
	record.Revision = upload.ID
	record.UpdatedAt = time.Now().UTC()
	history, err := record.historyDir()
	if err != nil {
		finishError(err)
		return
	}
	if err = copyPublicationDirectory(filepath.Join(stage, "history"), history, 0700); err != nil {
		finishError(err)
		return
	}
	summary, err := api.publicationSummary(ctx, upload.ActorID, upload.ID, &record, manifest.Handoff)
	if err != nil {
		summary = "Summary preparation was unavailable. The following private handoff transcript is the fallback; full raw chat is retained.\n\n" + manifest.Handoff
		log.Printf("[CREW_PUBLISH] operation=%s summary=transcript_fallback", upload.ID)
	}
	if err = os.WriteFile(filepath.Join(history, "summary.md"), []byte(summary), 0600); err != nil {
		finishError(err)
		return
	}

	// Imported functions join the Crew's existing entry points without replacing
	// another agent's function or the destination's authored functions.
	{
		var incoming projectFunctionsDocument
		raw, e := os.ReadFile(filepath.Join(stage, "source", crewFunctionsFileName))
		if e == nil {
			_ = json.Unmarshal(raw, &incoming)
		} else if !errors.Is(e, os.ErrNotExist) {
			finishError(e)
			return
		}
		existing, e := readCrewFunctions(contextWithoutPublication(ctx), triggerTarget{Kind: triggerCallerCrew, Path: record.Root, CrewID: record.WorkspaceID, CrewProfile: record.ProfileID, CrewOwner: record.OwnerID})
		if e != nil {
			finishError(e)
			return
		}
		for _, fn := range incoming.Functions {
			for _, prior := range existing {
				if prior.Name == fn.Name && (prior.publication == nil && record.AgentID != "" || prior.publication != nil && prior.publication.AgentID != record.AgentID) {
					finishError(fmt.Errorf("Function %q already exists in this Crew. Rename the incoming function and publish again; the existing Crew was kept.", fn.Name))
					return
				}
			}
		}
	}
	if err = validatePublishedFunctions(filepath.Join(stage, "source", "functions.json")); err != nil {
		finishError(err)
		return
	}
	source := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(record.sourceRoot()))
	if active, e := readPublishedAgent(ctx, record.Root, record.AgentID); e != nil {
		finishError(e)
		return
	} else if active != nil && active.Revision == record.Revision {
		upload.Record = active
		upload.State = "live"
		_ = savePublicationUpload(upload)
		return
	}
	if err = os.RemoveAll(filepath.Dir(source)); err != nil {
		finishError(err)
		return
	}
	if err = copyPublicationDirectory(filepath.Join(stage, "source"), source, 0755); err != nil {
		finishError(err)
		return
	}
	if err = preparePublicationRuntime(ctx, &record, source, manifest); err != nil {
		finishError(err)
		return
	}
	if latest, e := loadPublicationUpload(upload.ID, upload.ActorID); e == nil && latest.State == "deleted" {
		_ = os.RemoveAll(history)
		_ = os.RemoveAll(filepath.Dir(source))
		return
	}
	// Recheck authority and the disclosed company account immediately before activation.
	binding, _, allowed := api.resolveExternalWorkspace(ctx, &claims, record.ProfileID+":"+record.WorkspaceID)
	if !allowed || !binding.OwnedByCaller || !publicationOriginTokenAllowed(&claims, record.ProfileID, record.WorkspaceID) {
		finishError(fmt.Errorf("Builder access changed; your previous version is still live."))
		return
	}
	if _, err = api.admitProviderAccount(ctx, providerAccountScope{Principal: record.OwnerID, WorkspacePath: record.Root, Product: record.ProfileID}, record.Provider, record.ConnectionID); err != nil {
		finishError(err)
		return
	}
	// One saved pointer activates source/functions together. Existing calls keep their old revision path.
	if err = savePublishedProject(&record); err != nil {
		finishError(err)
		return
	}
	defer lockPublication(upload.ID)()
	if target, e := api.dashboardTarget(ctx, &claims, record.Root); e == nil {
		if e := api.refreshPublicationDashboard(ctx, &claims, target); e != nil {
			// Activation is already saved. Keep the working publication live
			// and identify the dashboard follow-up without exposing content.
			log.Printf("[CREW_PUBLISH] operation=%s revision=%s dashboard_refresh=failed", upload.ID, record.Revision)
		}
		if audience, e := api.readRunShare(ctx, &claims, target); e == nil {
			upload.Audience = &audience
		}
	}
	upload.Record = &record
	upload.WorkspaceID = record.WorkspaceID
	upload.FileCount = len(manifest.Files)
	upload.Excluded = manifest.Excluded
	upload.RawBytes = manifest.History.CapturedBytes
	upload.RawSHA256 = manifest.History.SHA256
	upload.State = "live"
	if record.AgentID != "" {
		if _, tabErr := api.openPublishedAgent(httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx), &record); tabErr != nil {
			upload.TabError = tabErr.Error()
		}
	}
	upload.UpdatedAt = time.Now().UTC()
	if err = savePublicationUpload(upload); err != nil {
		log.Printf("[CREW_PUBLISH] operation=%s revision=%s activation_saved receipt_error=true", upload.ID, record.Revision)
		return
	}
	log.Printf("[CREW_PUBLISH] operation=%s actor=%s workspace=%s:%s revision=%s state=live files=%d excluded=%d raw_bytes=%d raw_sha256=%s native_resume=not_attempted", upload.ID, sanitizeUserIDForPath(upload.ActorID), record.ProfileID, record.WorkspaceID, record.Revision, upload.FileCount, len(upload.Excluded), upload.RawBytes, upload.RawSHA256)
}

func validatePublishedFunctions(file string) error {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc projectFunctionsDocument
	if json.Unmarshal(raw, &doc) != nil {
		return fmt.Errorf("functions.json must contain a supported functions document")
	}
	seen := map[string]bool{}
	identities := map[string]bool{}
	for _, record := range doc.Functions {
		if record.ID == "" || identities[record.ID] {
			return fmt.Errorf("each published function needs a unique id")
		}
		identities[record.ID] = true
		if record.Action.Type != "message" || record.InputMapping != nil || len(record.AllowedCallers) > 0 {
			return fmt.Errorf("published Crew functions need a message action; caller access comes from Share")
		}
		fn, err := crewFunctionExecutionView(record)
		if err != nil {
			return fmt.Errorf("function %s: %w", record.Name, err)
		}
		if seen[fn.Name] {
			return fmt.Errorf("Function %q is declared twice. Give each function a distinct name, then publish again.", fn.Name)
		}
		seen[fn.Name] = true
		if err := (crewFunctionSpec{Name: fn.Name, Description: fn.Description, Instructions: fn.Instructions, InputSchema: fn.InputSchema, ResultSchema: fn.ResultSchema, Execution: fn.Execution, Agent: fn.Agent}).validate(); err != nil {
			return err
		}
	}
	return nil
}

func copyPublicationDirectory(from, to string, mode fs.FileMode) error {
	if err := os.MkdirAll(to, mode); err != nil {
		return err
	}
	return filepath.WalkDir(from, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == from {
			return nil
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, mode)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("publication contains a non-regular file")
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		perm := fs.FileMode(0644)
		if mode == 0700 {
			perm = 0600
		}
		if info.Mode()&0111 != 0 {
			perm = 0755
		}
		return os.WriteFile(dest, data, perm)
	})
}

func preparePublicationRuntime(ctx context.Context, record *publishedProject, source string, manifest publication.Manifest) error {
	record.SourcePaths = nil
	for _, file := range manifest.Files {
		if file.Role != "runtime_seed" {
			record.SourcePaths = append(record.SourcePaths, file.Path)
		}
	}
	history, err := record.historyDir()
	if err != nil {
		return err
	}
	journal := filepath.Join(history, "runtime-seeds.json")
	seeds := map[string]string{}
	if raw, e := os.ReadFile(journal); e == nil {
		if json.Unmarshal(raw, &seeds) != nil {
			return fmt.Errorf("This publication's runtime seed receipt is invalid; start a new publication.")
		}
	}
	// Seed only missing files on the first publication. A republish never resets live data.
	active, err := readPublishedAgent(ctx, record.Root, record.AgentID)
	if err != nil {
		return err
	}
	for _, rel := range []string{"db", "assets", "reports", "tool_output_folder", ".calls", "shared", "outputs"} {
		if err := createWorkspaceFolder(ctx, record.runtimeRoot()+"/"+rel); err != nil {
			return err
		}
	}
	if active == nil {
		// Report collisions before copying any seed. The receipt makes a lost
		// response retry of this same operation safe without resetting data.
		for _, file := range manifest.Files {
			if file.Role != "runtime_seed" {
				continue
			}
			to := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(record.runtimeRoot()), filepath.FromSlash(file.Path))
			if _, e := os.Lstat(to); e == nil && seeds[file.Path] != file.SHA256 {
				return fmt.Errorf("Runtime seed %q already exists. Exclude it or use a new Crew; live data was kept.", file.Path)
			} else if e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
			seeds[file.Path] = file.SHA256
		}
		if err := fsutil.WriteJSONAtomic(journal, seeds, 0600); err != nil {
			return err
		}
	}
	for _, file := range manifest.Files {
		if file.Role != "runtime_seed" {
			continue
		}
		from := filepath.Join(source, filepath.FromSlash(file.Path))
		if active == nil {
			to := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(record.runtimeRoot()), filepath.FromSlash(file.Path))
			if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if errors.Is(err, os.ErrExist) {
				raw, e := os.ReadFile(to)
				sum := sha256.Sum256(raw)
				if e != nil || hex.EncodeToString(sum[:]) != seeds[file.Path] {
					return fmt.Errorf("Runtime data %q changed since this publication. Start a new publication; live data was kept.", file.Path)
				}
				if err := os.Remove(from); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(from)
			if err == nil {
				_, err = out.Write(raw)
			}
			closeErr := out.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		if err := os.Remove(from); err != nil {
			return err
		}
	}
	// Source categories retain their immutable directories. Empty runtime categories are links
	// for ordinary scripts; a mixed source/runtime folder uses explicit AGENTWORKS_RUNTIME_ROOT.
	for _, rel := range []string{"db", "assets", "reports", "tool_output_folder", ".calls", "shared", "outputs"} {
		seed := filepath.Join(source, rel)
		_ = os.Remove(seed) // succeeds only for an empty directory
		if _, err := os.Lstat(seed); errors.Is(err, os.ErrNotExist) {
			stable := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(record.runtimeRoot()), rel)
			if err := os.Symlink(stable, seed); err != nil {
				return err
			}
		}
	}
	return nil
}

func publicationTokenPath(r *http.Request) bool {
	if r.URL.Path == "/api/external/v1/publication-history" {
		return r.Method == http.MethodGet
	}
	const prefix = "/api/external/v1/publications"
	if r.URL.Path == prefix {
		return r.Method == http.MethodPost
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return false
	}
	id := strings.TrimPrefix(r.URL.Path, prefix+"/")
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	return r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodPost
}

func publicationOriginTokenAllowed(claims *UserClaims, profile, id string) bool {
	if claims.AccessToken == nil {
		return true
	}

	return claims.AccessToken.Allows("crews:write") && (id == "" && claims.AccessToken.AllCrews || id != "" && claims.AccessToken.AllowsCrew(id))
}
