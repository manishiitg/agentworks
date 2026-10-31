package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewSyncRequest struct {
	Action   string             `json:"action"`
	PairID   string             `json:"pair_id,omitempty"`
	SourceID string             `json:"source_id,omitempty"`
	Name     string             `json:"name,omitempty"`
	Editing  string             `json:"editing,omitempty"`
	Snapshot *crewsync.Snapshot `json:"snapshot,omitempty"`
}
type crewSyncReply struct {
	ProjectID string                `json:"project_id"`
	Binding   *crewroot.SyncBinding `json:"binding"`
	Snapshot  *crewsync.Snapshot    `json:"snapshot,omitempty"`
}

func crewSyncManifestPath(id string) (string, error) {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	if _, err = crewroot.PrivateRoot(state, id); err != nil {
		return "", err
	}
	return filepath.Join(state, "crew-sync", id+"-files.json"), nil
}
func readCrewSyncManifest(id string) (crewsync.Manifest, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return crewsync.Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var files crewsync.Manifest
	err = json.Unmarshal(b, &files)
	return files, err
}
func saveCrewSyncManifest(id string, files crewsync.Manifest) error {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(p, files, 0600)
}

func (api *StreamingAPI) crewSyncRoots(ctx context.Context, claims *UserClaims, id string) (crewroot.Roots, error) {
	if code, message := externalCrewAuthorGate(claims); code != 0 {
		return crewroot.Roots{}, fmt.Errorf("%s", message)
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(id))) {
		return crewroot.Roots{}, fmt.Errorf("this connection cannot sync this Crew")
	}
	binding, _, ok := api.resolveExternalWorkspace(ctx, claims, id)
	if !ok || !binding.OwnedByCaller || binding.OwnerID != claims.UserID {
		return crewroot.Roots{}, fmt.Errorf("only this Crew's main owner can sync it")
	}
	roots, found, err := crewStorageRoots(binding.Binding.WorkspacePath)
	if err != nil {
		return roots, err
	}
	if !found {
		return roots, fmt.Errorf("this Crew's folder is unavailable")
	}
	return roots, nil
}

// The transport carries project content, never paths or credentials chosen by
// another machine. Every operation rechecks the destination's actual owner.
func (api *StreamingAPI) handleCrewSync(w http.ResponseWriter, r *http.Request) {
	var input crewSyncRequest
	if r.Method == http.MethodGet {
		input.Action = "status"
	} else if json.NewDecoder(io.LimitReader(r.Body, 360<<20)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Send a valid sync request.")
		return
	}
	out, err := api.crewSync(r.Context(), GetUserFromContext(r.Context()), mux.Vars(r)["project"], input)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) crewSync(ctx context.Context, claims *UserClaims, id string, input crewSyncRequest) (crewSyncReply, error) {
	if input.Action == "create" {
		if input.PairID == "" || input.SourceID == "" || input.Name == "" {
			return crewSyncReply{}, fmt.Errorf("choose a local Crew to sync")
		}
		if !workspaceCreateTokenAllowed(claims) {
			return crewSyncReply{}, fmt.Errorf("this connection cannot create a Crew")
		}
		created, err := api.createWorkspaceFromSpec(ctx, claims, crewProfileID, crewSpec{Name: input.Name}, false)
		if err != nil {
			return crewSyncReply{}, err
		}
		roots, err := api.crewSyncRoots(ctx, claims, created)
		if err != nil {
			return crewSyncReply{}, err
		}
		if err = os.MkdirAll(filepath.Join(roots.Files, crewroot.FilesSegment), 0770); err != nil {
			return crewSyncReply{}, err
		}
		binding := crewroot.SyncBinding{PairID: input.PairID, Side: "server", Editing: "local", PeerProjectID: input.SourceID}
		if err = crewroot.WriteSync(created, binding); err != nil {
			return crewSyncReply{}, err
		}
		return crewSyncReply{ProjectID: created, Binding: &binding}, nil
	}
	roots, err := api.crewSyncRoots(ctx, claims, id)
	if err != nil {
		return crewSyncReply{}, err
	}
	unlock, err := wf.LockWorkspace(ctx, roots.Files)
	if err != nil {
		return crewSyncReply{}, err
	}
	defer unlock()
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: roots.ID, Binding: binding}
	if input.Action == "status" {
		return out, nil
	}
	if binding == nil || input.PairID != binding.PairID || binding.Side != "server" {
		return out, fmt.Errorf("choose the server paired with this Crew")
	}
	previous, err := readCrewSyncManifest(roots.ID)
	if err != nil {
		return out, err
	}
	switch input.Action {
	case "push":
		if binding.Editing != "local" || input.Snapshot == nil {
			return out, fmt.Errorf("editing is on the server; bring its changes back before updating it")
		}
		if input.Editing != "local" && input.Editing != "server" {
			return out, fmt.Errorf("choose where to work")
		}
		if err = crewsync.Apply(ctx, roots, *input.Snapshot); err != nil {
			return out, err
		}
		if err = saveCrewSyncSnapshot(roots.ID, *input.Snapshot); err != nil {
			return out, err
		}
		binding.Editing = input.Editing
	case "pull":
		// Freeze authored writes before capturing the return copy. Retrying a
		// failed return keeps the same side selected; it never discards files.
		binding.Editing = "local"
		if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
			return out, err
		}
		snapshot, captureErr := captureCrewSyncSnapshot(ctx, roots, previous)
		if captureErr != nil {
			return out, captureErr
		}
		out.Snapshot = &snapshot
		// The receiver acknowledges only after its conflict checks and writes.
		return out, nil
	case "ack":
		if binding.Editing != "local" || input.Snapshot == nil {
			return out, fmt.Errorf("finish returning this Crew to your Mac first")
		}
		if err = saveCrewSyncSnapshot(roots.ID, *input.Snapshot); err != nil {
			return out, err
		}
	default:
		return out, fmt.Errorf("unknown sync action")
	}
	binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
		return out, err
	}
	return out, nil
}

func readCrewSyncGit(id string) (*crewsync.GitSnapshot, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p + ".git")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state crewsync.GitSnapshot
	err = json.Unmarshal(b, &state)
	return &state, err
}
func captureCrewSyncSnapshot(ctx context.Context, roots crewroot.Roots, previous crewsync.Manifest) (crewsync.Snapshot, error) {
	snapshot, err := crewsync.Capture(ctx, roots, previous)
	if err != nil {
		return snapshot, err
	}
	prior, err := readCrewSyncGit(roots.ID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Git, err = crewsync.CaptureGit(ctx, roots.Files, prior)
	return snapshot, err
}
func saveCrewSyncSnapshot(id string, snapshot crewsync.Snapshot) error {
	if err := saveCrewSyncManifest(id, snapshot.Files); err != nil {
		return err
	}
	if snapshot.Git == nil {
		return nil
	}
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return err
	}
	state := *snapshot.Git
	state.Bundle = nil
	return fsutil.WriteJSONAtomic(p+".git", state, 0600)
}
