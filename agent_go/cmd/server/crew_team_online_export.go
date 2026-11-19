package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"slices"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

type teamOnlineExportCommand struct {
	Request     teamOnlineRequest `json:"request"`
	Token       string            `json:"token,omitempty"`
	Chunks      []string          `json:"chunks,omitempty"`
	IncludeData bool              `json:"include_data"`
	// remove: where the moved workflow went, for the source's note.
	Moved *teamOnlineMovedWorkflow `json:"moved,omitempty"`
}

func (api *StreamingAPI) prepareTeamOnlineExport(ctx context.Context, source triggerTarget, in teamOnlineRequest, includeData bool) (*teamOnlinePackage, error) {
	claims := GetUserFromContext(ctx)
	if !crewTeamClaims(claims, source.CrewID, true) {
		return nil, fmt.Errorf("allow the source Crew in your Builder connection")
	}
	if source.CrewOwner != GetUserIDFromContext(ctx) || source.CrewID != in.SourceProjectID {
		return nil, fmt.Errorf("choose an item in your own source Crew")
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil {
		for _, name := range []string{"schedules.json", "functions.json", "code/triggers", "db"} {
			if !token.FileGuard.Allows(name, false) {
				return nil, fmt.Errorf("this connection does not allow the source item files")
			}
		}
	}
	p, err := newTeamOnlinePackage(source.CrewID)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			p.close()
		}
	}()
	p.owner = source.CrewOwner
	p.sourceLocal = source
	p.request = in
	p.manifest.SourceProjectID = source.CrewID
	ids := slices.Clone(in.IncludeAutomations)
	functionIDs := []string{}
	if in.Kind == "automation" {
		ids = []string{in.ItemID}
	} else if in.Kind == "workflow" {
		if ids, err = api.exportTeamOnlineWorkflow(ctx, source, p); err != nil {
			return nil, err
		}
	} else {
		ref := teamOnlineDashboardRef{ItemID: in.ItemID, Kind: in.Kind, Revision: in.Revision, SubmissionID: in.SubmissionID}
		item, err := api.exportTeamOnlineDashboard(ctx, source, ref, p.root, includeData)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if !slices.ContainsFunc(item.Dependencies.Automations, func(a teamonline.Automation) bool { return a.ID == id }) {
				return nil, fmt.Errorf("choose only automations from this item's dependency preview")
			}
		}
		functionIDs = item.Dependencies.Functions
		if err = teamOnlineBindDashboardFunctions(source, p); err != nil {
			return nil, err
		}
	}
	if err = api.exportTeamOnlineAutomations(ctx, source, ids, functionIDs, p); err != nil {
		return nil, err
	}
	if err = p.index(); err != nil {
		return nil, err
	}
	if err = api.scanTeamOnlinePackage(ctx, source, p); err != nil {
		reason := "package_invalid"
		if err.Error() == "secret_match" {
			reason = "secret_match"
		}
		log.Printf("[TEAM_ONLINE] event=export_refused source_project=%s kind=%s item_id=%s revision=%s reason=%s", source.CrewID, in.Kind, in.ItemID, in.Revision, reason)
		return nil, fmt.Errorf("the item contains data that cannot be shared; remove credential values and try again")
	}
	var size int64
	for _, file := range p.manifest.Files {
		size += file.Size
	}
	log.Printf("[TEAM_ONLINE] event=export source_project=%s kind=%s item_id=%s revision=%s bytes=%d file_count=%d", source.CrewID, in.Kind, in.ItemID, in.Revision, size, len(p.manifest.Files))
	ok = true
	return p, nil
}

// Called only after runDesktopCrewChat has checked the exact local pair and
// derived the local owner. The server cannot use this to read a different Crew.
func (api *StreamingAPI) desktopTeamOnlineExport(ctx context.Context, claims *UserClaims, project string, call crewChatCall) crewChatResult {
	reply := func(status int, value any) crewChatResult {
		raw, _ := json.Marshal(value)
		return crewChatResult{ID: call.ID, Status: status, Body: raw}
	}
	fail := func(err error) crewChatResult { return reply(409, map[string]string{"error": err.Error()}) }
	var command teamOnlineExportCommand
	if err := json.Unmarshal(call.Args, &command); err != nil {
		return fail(fmt.Errorf("invalid item export"))
	}
	in := command.Request
	if err := in.validate(); err != nil {
		return fail(err)
	}
	_, binding, _, bindErr := api.desktopCrewChatBinding(ctx, project)
	canonicalAutomation := bindErr == nil && in.Kind == "automation" && in.SourceProjectID == in.TeamProjectID && binding.PeerProjectID == in.TeamProjectID
	if in.SourceProjectID != project && !canonicalAutomation {
		return fail(fmt.Errorf("choose an item in this Mac's own paired Crew"))
	}
	source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+project)
	if err != nil || source.CrewOwner != claims.UserID {
		return fail(fmt.Errorf("the source Crew is unavailable"))
	}
	if call.Action == "prepare" {
		localInput := in
		localInput.SourceProjectID = project
		p, err := api.prepareTeamOnlineExport(ctx, source, localInput, command.IncludeData)
		if err != nil {
			return fail(err)
		}
		p.request = in
		p.manifest.SourceProjectID = in.SourceProjectID
		retainTeamOnlineExport(p)
		return reply(200, p.manifest)
	}
	value, ok := teamOnlineExports.Load(command.Token)
	if !ok {
		return fail(fmt.Errorf("the item export expired; preview it again"))
	}
	p := value.(*teamOnlinePackage)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner != claims.UserID || p.request.SourceProjectID != in.SourceProjectID || p.request.ItemID != in.ItemID || p.request.Kind != in.Kind || p.request.Revision != in.Revision || p.request.SubmissionID != in.SubmissionID || p.request.TeamProjectID != in.TeamProjectID || time.Now().After(p.expires) {
		return fail(fmt.Errorf("this export belongs to another hand-off"))
	}
	switch call.Action {
	case "chunks":
		var frames bytes.Buffer
		var size int64
		if len(command.Chunks) > 256 {
			return fail(fmt.Errorf("request a bounded chunk batch"))
		}
		for _, id := range command.Chunks {
			admitted := false
			for _, file := range p.manifest.Files {
				if slices.ContainsFunc(file.Chunks, func(c chunks.Chunk) bool { return c.ID == id }) {
					admitted = true
					break
				}
			}
			if !admitted || !chunks.ValidID(id) {
				return fail(fmt.Errorf("choose a chunk from this item export"))
			}
			file, err := p.chunkRoot.Open(id)
			if err != nil {
				return fail(err)
			}
			data, err := io.ReadAll(io.LimitReader(file, chunks.MaxBytes+1))
			file.Close()
			if err != nil {
				return fail(err)
			}
			size += int64(len(data))
			if size > chunks.BatchBytes {
				return fail(fmt.Errorf("request a bounded chunk batch"))
			}
			if err = chunks.WriteFrame(&frames, id, data); err != nil {
				return fail(err)
			}
		}
		return reply(200, map[string]string{"frames": base64.StdEncoding.EncodeToString(frames.Bytes())})
	case "stop", "restart":
		if err := stopTeamOnlineSource(ctx, source, p, call.Action == "restart"); err != nil {
			return fail(err)
		}
		p.sourceStopped = call.Action == "stop"
		return reply(200, map[string]bool{"stopped": true})
	case "complete":
		p.sourceStopped = false
		return reply(200, map[string]bool{"completed": true})
	case "remove":
		if command.Moved == nil {
			return fail(fmt.Errorf("say where the workflow moved"))
		}
		if err := api.removeMovedWorkflowSource(ctx, source, p, *command.Moved); err != nil {
			return fail(err)
		}
		p.sourceStopped = false
		return reply(200, map[string]bool{"removed": true})
	case "close":
		if p.sourceStopped {
			if err := stopTeamOnlineSource(ctx, source, p, true); err != nil {
				return fail(err)
			}
			p.sourceStopped = false
		}
		teamOnlineExports.Delete(command.Token)
		p.close()
		return reply(200, map[string]bool{"closed": true})
	default:
		return fail(fmt.Errorf("choose prepare, chunks, stop or close"))
	}
}

// The transport closure pins the exact caller's current pair for the entire
// hand-off; it never queues, reads a mirror or tries another person's computer.
func (api *StreamingAPI) fetchTeamOnlineSource(ctx context.Context, in teamOnlineRequest, includeData bool) (triggerTarget, *teamOnlinePackage, func(string) error, func(), error) {
	person := GetUserIDFromContext(ctx)
	claims := GetUserFromContext(ctx)
	pairProject := in.SourceProjectID
	pair, mac := crewMacPairFor(person, pairProject)
	if !mac && ownerOfCrewProjectID(in.SourceProjectID) == "" {
		pairProject = in.TeamProjectID
		pair, mac = crewMacPairFor(person, pairProject)
	}
	if !mac {
		source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+in.SourceProjectID)
		if err != nil || source.CrewOwner != person {
			return triggerTarget{}, nil, nil, nil, fmt.Errorf("choose one of your own source Crews")
		}
		p, err := api.prepareTeamOnlineExport(ctx, source, in, includeData)
		if err != nil {
			return source, nil, nil, nil, err
		}
		return source, p, func(action string) error {
			if action == "complete" {
				return nil
			}
			if action == "remove" {
				return api.removeMovedWorkflowSource(ctx, source, p, *p.moved)
			}
			return stopTeamOnlineSource(ctx, source, p, action == "restart")
		}, p.close, nil
	}
	if crewMacSocketFor(person, pairProject, pair) == nil {
		return triggerTarget{}, nil, nil, nil, fmt.Errorf("The source computer is offline. Open the app there and try again.")
	}
	var received *teamOnlinePackage
	call := func(action, token string, ids []string, out any) error {
		command := teamOnlineExportCommand{Request: in, Token: token, Chunks: ids, IncludeData: includeData}
		if action == "remove" && received != nil {
			command.Moved = received.moved
		}
		raw, _ := json.Marshal(command)
		result := callCrewMacPair(ctx, person, pairProject, pair, crewChatCall{Operation: "team_online_export", Action: action, Args: raw}, 2*time.Minute)
		if result.Status != 200 {
			var failure struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(result.Body, &failure)
			if failure.Error != "" {
				return fmt.Errorf("%s", failure.Error)
			}
			return fmt.Errorf("The source computer is offline. Open the app there and try again.")
		}
		return json.Unmarshal(result.Body, out)
	}
	var manifest teamOnlinePackageManifest
	if err := call("prepare", "", nil, &manifest); err != nil {
		return triggerTarget{}, nil, nil, nil, err
	}
	closeRemote := func() { var ignored any; _ = call("close", manifest.Token, nil, &ignored) }
	if manifest.SourceProjectID != in.SourceProjectID {
		closeRemote()
		return triggerTarget{}, nil, nil, nil, fmt.Errorf("the source export does not match this Crew")
	}
	p, err := receiveTeamOnlinePackage(in.TeamProjectID, manifest, func(ids []string) ([]byte, error) {
		var out struct {
			Frames string `json:"frames"`
		}
		if err := call("chunks", manifest.Token, ids, &out); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(out.Frames)
	})
	if err != nil {
		closeRemote()
		return triggerTarget{}, nil, nil, nil, err
	}
	p.owner = person
	p.request = in
	received = p
	source := triggerTarget{Kind: triggerCallerCrew, CrewID: manifest.SourceProjectID, CrewOwner: person, CrewProfile: crewProfileID}
	return source, p, func(action string) error {
		var ignored any
		return call(action, manifest.Token, nil, &ignored)
	}, func() { p.close(); closeRemote() }, nil
}
