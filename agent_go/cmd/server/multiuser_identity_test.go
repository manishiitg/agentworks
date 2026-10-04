package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// identityRows are the turn kinds whose CLI identity is pinned. The same rows run against every layout.
func (f *multiUserFixture) identityRows() []struct {
	name       string
	in         turnRunAsInput
	readOnly   bool
	workflow   bool
	wantUser   string
	wantSlot   string
	pathRule   string // the slot the pre-PLAT-442 folder rule gives the CLI's real starting folder
	sameAsPath bool
} {
	e := todayIdentity
	crewA := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	codeA := f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)
	userChats := func(u string) string { return perUserChatsFolderFor(u) }
	// The expected User for a row follows from the expected slot: slot of A -> A, slot of B -> B.
	owner := func(slot string) string {
		switch slot {
		case fixtureSlotA:
			return fixtureUserA
		case fixtureSlotB:
			return fixtureUserB
		}
		return ""
	}
	type row = struct {
		name       string
		in         turnRunAsInput
		readOnly   bool
		workflow   bool
		wantUser   string
		wantSlot   string
		pathRule   string
		sameAsPath bool
	}
	return []row{
		{name: "Code, A's turn", in: turnRunAsInput{ProfileID: "code", WorkingFolder: codeA, CallerID: fixtureUserA}, wantUser: owner(e.CodeOwnerSlot), wantSlot: e.CodeOwnerSlot},
		{name: "Crew, A's (owner) turn", in: turnRunAsInput{ProfileID: "work", WorkingFolder: crewA, CallerID: fixtureUserA}, wantUser: owner(e.CrewOwnerSlot), wantSlot: e.CrewOwnerSlot},
		{name: "Crew, B's Run-mode (reader) turn", in: turnRunAsInput{ProfileID: "work", WorkingFolder: crewA, CallerID: fixtureUserB}, readOnly: true, wantUser: owner(e.CrewReaderSlot), wantSlot: e.CrewReaderSlot},
		{name: "Goal, A (owner) builder turn", in: turnRunAsInput{WorkingFolder: fixtureGoal, CallerID: fixtureUserA}, workflow: true, wantSlot: e.GoalSlot},
		{name: "Goal, B (reader) run turn", in: turnRunAsInput{WorkingFolder: fixtureGoal, CallerID: fixtureUserB}, workflow: true, readOnly: true, wantSlot: e.GoalSlot},
		{name: "private chat, A", in: turnRunAsInput{WorkingFolder: userChats(fixtureUserA), CallerID: fixtureUserA}, wantUser: fixtureUserA, wantSlot: fixtureSlotA},
		{name: "private chat, B", in: turnRunAsInput{WorkingFolder: userChats(fixtureUserB), CallerID: fixtureUserB}, wantUser: fixtureUserB, wantSlot: fixtureSlotB},
	}
}

// TestRunAsRegressionTable is the PLAT-442 step 2 regression guard at the application level. For each turn kind it
// builds the folder the CLI really starts in (the same code the chat handler runs), asks what the platform declares,
// and checks that against what the old folder rule would have decided for that folder: they must be identical, so
// naming the identity changed nobody's account.
func TestRunAsRegressionTable(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	cfg := slots.ExecConfig{
		DocsRoot:      f.Docs,
		SlotTable:     os.Getenv("AGENTWORKS_SLOTS_FILE"),
		SlotStateRoot: filepath.Join(filepath.Dir(f.Docs), "slot-state"),
		SlotRunRoot:   filepath.Join(filepath.Dir(f.Docs), "slot-run"),
	}
	for _, row := range f.identityRows() {
		t.Run(row.name, func(t *testing.T) {
			in := row.in
			cliDir, shared, err := turnCLIWorkingDir(in.ProfileID, row.workflow, in.WorkingFolder, in.CallerID, "session-1", "claude-code", "run", row.readOnly)
			if err != nil {
				t.Fatalf("working dir: %v", err)
			}
			in.WorkflowPhase, in.CLIWorkingDir, in.SharedWorkingDir = row.workflow, cliDir, shared
			got := decideTurnRunAs(f.Ctx(in.CallerID), in, defaultRunAsDeps())
			if !got.Declared {
				t.Fatalf("not declared: %+v", got)
			}
			if got.User != row.wantUser || got.Slot != row.wantSlot {
				t.Fatalf("declared user %q slot %q, want user %q slot %q (cli folder %s)", got.User, got.Slot, row.wantUser, row.wantSlot, cliDir)
			}
			if got.Root != filepath.Clean(cliDir) {
				t.Fatalf("declared root %q, want %q", got.Root, cliDir)
			}
			// Behaviour unchanged: the old folder rule on the same starting folder names the same slot.
			if rule := cfg.SlotForDir(cliDir); rule != got.Slot {
				t.Fatalf("folder rule says %q for %s, the platform declares %q", rule, cliDir, got.Slot)
			}
		})
	}
}

// The owner of a project comes from the server's registry, then from the path, and NEVER from the manifest
// (PLAT-449): a Code stored in B's tree whose manifest claims A is B's, and a project registered to A stays A's
// wherever a copy of it sits.
func TestRunAsOwnerComesFromTheRegistryThenThePathNeverTheManifest(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	odd := workspaceCodeRootForTest(fixtureUserB, "moved-c0de0002")
	f.put(odd+"/product.json", `{"schema_version":1,"product":"code","id":"c0de0002-0000","owner_id":"`+fixtureUserA+`"}`)
	in := turnRunAsInput{ProfileID: "code", WorkingFolder: odd, CallerID: fixtureUserB, CLIWorkingDir: "/x/cli", SharedWorkingDir: "/x/cli"}
	// Unregistered: the path owner (B), whatever the manifest claims.
	if got := decideTurnRunAs(f.Ctx(fixtureUserB), in, defaultRunAsDeps()); got.User != fixtureUserB || got.Slot != fixtureSlotB {
		t.Fatalf("the manifest's claim decided the owner: %+v", got)
	}
	// Registered to A (the server created it for A): A, and B's launch is refused before it starts.
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "code", Folder: "moved-c0de0002", OwnerID: fixtureUserA}); err != nil {
		t.Fatal(err)
	}
	if got := decideTurnRunAs(f.Ctx(fixtureUserB), in, defaultRunAsDeps()); got.User != fixtureUserA || got.Slot != fixtureSlotA {
		t.Fatalf("registered owner ignored: %+v", got)
	}
	if err := checkProjectLaunchOwner(f.Ctx(fixtureUserB), in, defaultRunAsDeps()); !errors.Is(err, errProjectOwnerMismatch) {
		t.Fatalf("B's launch in a project registered to A was not refused: %v", err)
	}
}

func workspaceCodeRootForTest(owner, folder string) string {
	return legacyIdentityLayout().CodePhysical(owner, folder)
}

// TestMultiUserAccess runs the privacy assertions against the fixture's layout.
func TestMultiUserAccess(t *testing.T) {
	runMultiUserAccessAssertions(t, legacyIdentityLayout())
}

// runMultiUserAccessAssertions is the reusable half of the fixture: the Crew move runs it with its own layout.
func runMultiUserAccessAssertions(t *testing.T, layout identityLayout) {
	f := newMultiUserFixture(t, layout)
	codePhysical := layout.CodePhysical(fixtureUserA, fixtureCodeFolder)
	codeShort := layout.CodeShort(fixtureCodeFolder)
	crewPhysical := layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	crewShort := layout.CrewShort(fixtureCrewFolder)

	t.Run("B cannot reach A's Code by either spelling", func(t *testing.T) {
		for _, hint := range []string{"", codePhysical, codeShort} {
			if got, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, f.Code, fixtureCodeID, hint); err == nil {
				t.Fatalf("B resolved A's Code (hint %q): %+v", hint, got)
			}
		}
		for _, folder := range []string{codePhysical, codeShort} {
			req := QueryRequest{AgentProfileID: "code", AgentProfileConversationKey: fixtureCodeID, SelectedFolder: folder}
			if level, err := f.API.conversationTargetAccess(f.Ctx(fixtureUserB), req); err == nil || level != WorkflowAccessNone {
				t.Fatalf("B reached A's Code with folder %q: %v err=%v", folder, level, err)
			}
			// Naming only the folder is no way around it.
			req.AgentProfileID = ""
			if level, err := f.API.conversationTargetAccess(f.Ctx(fixtureUserB), req); err == nil || level != WorkflowAccessNone {
				t.Fatalf("B reached A's Code by folder alone %q: %v err=%v", folder, level, err)
			}
		}
		// The raw proxy: B's physical spelling is refused; the short spelling is B's OWN tree, so it passes the gate
		// but is not A's folder (nothing of A's lives there).
		if status := f.Proxy(fixtureUserB, http.MethodGet, codePhysical+"/code/main.go"); status != http.StatusForbidden {
			t.Fatalf("proxy let B read A's Code file: %d", status)
		}
		if status := f.Proxy(fixtureUserB, http.MethodPut, codePhysical+"/code/main.go"); status != http.StatusForbidden {
			t.Fatalf("proxy let B write A's Code file: %d", status)
		}
	})

	t.Run("A reaches A's Code by both spellings", func(t *testing.T) {
		for _, folder := range []string{codePhysical, codeShort} {
			req := QueryRequest{AgentProfileID: "code", AgentProfileConversationKey: fixtureCodeID, SelectedFolder: folder}
			if level, err := f.API.conversationTargetAccess(f.Ctx(fixtureUserA), req); err != nil || level != WorkflowAccessOwner {
				t.Fatalf("A with folder %q: %v err=%v", folder, level, err)
			}
		}
	})

	t.Run("B in Run mode reads A's Crew by the physical spelling and cannot write it", func(t *testing.T) {
		ref, ok := resolveCrewPath(f.Ctx(fixtureUserB), fixtureUserB, crewPhysical+"/code/notes.md")
		if !ok {
			t.Fatalf("physical crew path did not resolve")
		}
		if ref.OwnerID != fixtureUserA {
			t.Fatalf("owner of the resolved crew = %q, want A", ref.OwnerID)
		}
		if level := crewAccessFor(f.Claims(fixtureUserB), ref); level != crewAccessReader {
			t.Fatalf("B's access = %v, want reader", level)
		}
		// Raw proxy: reading is mediated (never raw), writing A's tree is refused outright.
		if status := f.Proxy(fixtureUserB, http.MethodPut, crewPhysical+"/code/notes.md"); status != http.StatusForbidden {
			t.Fatalf("proxy let B write A's Crew: %d", status)
		}
		if !isCrewReaderTurn(QueryRequest{AgentProfileID: "work", SelectedFolder: crewPhysical}, fixtureUserB) {
			t.Fatal("B's turn in A's Crew is not a reader turn")
		}
		read, write, _ := crewReaderWorkspaceRoots(crewPhysical, true)
		if len(write) != 0 || len(read) == 0 {
			t.Fatalf("reader roots read=%v write=%v", read, write)
		}
	})

	t.Run("A reaches A's Crew by the short spelling and owns it", func(t *testing.T) {
		ref, ok := resolveCrewPath(f.Ctx(fixtureUserA), fixtureUserA, crewShort+"/code/notes.md")
		if !ok || ref.OwnerID != fixtureUserA {
			t.Fatalf("short spelling: %+v ok=%v", ref, ok)
		}
		if level := crewAccessFor(f.Claims(fixtureUserA), ref); level != crewAccessOwner {
			t.Fatalf("A's access = %v, want owner", level)
		}
		if isCrewReaderTurn(QueryRequest{AgentProfileID: "work", SelectedFolder: crewShort}, fixtureUserA) {
			t.Fatal("A's own turn is a reader turn")
		}
	})

	t.Run("the Goal: A owns it, B reads it", func(t *testing.T) {
		if level, _ := workflowAccessForWorkspacePath(f.Ctx(fixtureUserA), f.Claims(fixtureUserA), fixtureGoal); level != WorkflowAccessOwner {
			t.Fatalf("A's Goal access = %v", level)
		}
		if level, _ := workflowAccessForWorkspacePath(f.Ctx(fixtureUserB), f.Claims(fixtureUserB), fixtureGoal); level != WorkflowAccessRead {
			t.Fatalf("B's Goal access = %v", level)
		}
		if status := f.Proxy(fixtureUserB, http.MethodGet, fixtureGoal+"/workflow.json"); status != 0 {
			t.Fatalf("proxy refused B's read of the Goal: %d", status)
		}
		if status := f.Proxy(fixtureUserB, http.MethodPut, fixtureGoal+"/planning/plan.json"); status != http.StatusForbidden {
			t.Fatalf("proxy let a reader write the Goal: %d", status)
		}
		if status := f.Proxy(fixtureUserA, http.MethodPut, fixtureGoal+"/planning/plan.json"); status != 0 {
			t.Fatalf("proxy refused the owner's write to the Goal: %d", status)
		}
	})

	t.Run("workspace proxy decision for each user and path", func(t *testing.T) {
		type pair struct {
			user, method, path string
			want               int // 0 allowed, else the refusing status
		}
		for _, p := range []pair{
			{fixtureUserA, http.MethodGet, codePhysical + "/code/main.go", 0},
			{fixtureUserA, http.MethodPut, codePhysical + "/code/main.go", 0},
			{fixtureUserB, http.MethodGet, codePhysical + "/code/main.go", http.StatusForbidden},
			{fixtureUserB, http.MethodPut, codePhysical + "/code/main.go", http.StatusForbidden},
			{fixtureUserA, http.MethodGet, crewPhysical + "/code/notes.md", 0},
			{fixtureUserA, http.MethodPut, crewPhysical + "/code/notes.md", 0},
			{fixtureUserB, http.MethodPut, crewPhysical + "/code/notes.md", http.StatusForbidden},
			{fixtureUserA, http.MethodGet, crewShort + "/code/notes.md", 0},
			{fixtureUserA, http.MethodGet, codeShort + "/code/main.go", 0},
		} {
			if got := f.Proxy(p.user, p.method, p.path); got != p.want {
				t.Errorf("%s %s as %s: proxy decision %d, want %d", p.method, p.path, p.user, got, p.want)
			}
		}
		// B reading A's Crew raw: refused today because every foreign _users path is. (Shared Crew roots add their
		// own gate in the Crew move; this row is where it must keep refusing.)
		if layout.CrewPhysical(fixtureUserA, fixtureCrewFolder) == "Crew/"+fixtureCrewFolder {
			return
		}
		if got := f.Proxy(fixtureUserB, http.MethodGet, crewPhysical+"/code/notes.md"); got != http.StatusForbidden {
			t.Errorf("raw proxy read of A's Crew by B: %d, want 403", got)
		}
	})
}

// sharedIdentityLayout is the Crew move's layout: a Crew at Crew/<folder> (both spellings the same), Code unchanged.
func sharedIdentityLayout() identityLayout {
	layout := legacyIdentityLayout()
	layout.CrewPhysical = func(_, folder string) string { return "Crew/" + folder }
	layout.CrewShort = func(folder string) string { return "Crew/" + folder }
	return layout
}

// PLAT-449: product.json is project data the owner (or an agent turn with write access to the project) can edit.
// Editing its owner_id, through the proxy or natively, must change neither who owns the project nor which Linux
// slot a launch uses.
func TestEditedManifestOwnerNeverChangesOwnershipOrLaunchIdentity(t *testing.T) {
	forged := func(product, id string) string {
		return `{"schema_version":1,"product":"` + product + `","id":"` + id + `","title":"x","session_id":"` + product + `:project:` + id + `","owner_id":"` + fixtureUserB + `"}`
	}
	t.Run("Code: A edits owner_id to B", func(t *testing.T) {
		f := newMultiUserFixture(t, legacyIdentityLayout())
		codeA := f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)
		// The browser path: A's write passes the proxy (it is A's own file), B's is refused.
		if got := f.Proxy(fixtureUserA, http.MethodPut, codeA+"/product.json"); got != 0 {
			t.Fatalf("the proxy refused A's own manifest write: %d", got)
		}
		if got := f.Proxy(fixtureUserB, http.MethodPut, codeA+"/product.json"); got != http.StatusForbidden {
			t.Fatalf("the proxy let B write A's manifest: %d", got)
		}
		// The native path: an edit in the folder itself (what a CLI turn or a shell does).
		f.put(codeA+"/product.json", forged("code", fixtureCodeID))

		profile := f.Code
		project, err := resolveCrewProjectBinding(f.Ctx(fixtureUserA), fixtureUserA, profile, fixtureCodeID, "")
		if err != nil || !project.OwnedByCaller || project.OwnerID != fixtureUserA {
			t.Fatalf("A no longer opens their Code: %+v err=%v", project, err)
		}
		if got := resolveProjectOwner(f.Ctx(fixtureUserA), codeA); got != fixtureUserA {
			t.Fatalf("resolved owner = %q after the edit", got)
		}
		in := turnRunAsInput{ProfileID: "code", WorkingFolder: codeA, CallerID: fixtureUserA, CLIWorkingDir: "/x/cli", SharedWorkingDir: "/x/cli"}
		runAs, err := declareTurnRunAs(f.Ctx(fixtureUserA), in)
		if err != nil || runAs.User != fixtureUserA || runAs.Slot != fixtureSlotA {
			t.Fatalf("launch identity after the edit: %+v err=%v", runAs, err)
		}
		if runAs.Slot == fixtureSlotB || runAs.User == fixtureUserB {
			t.Fatal("B's slot was selected")
		}
		// B cannot open it either, whatever the manifest says.
		if got, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, profile, fixtureCodeID, ""); err == nil {
			t.Fatalf("B opened A's Code: %+v", got)
		}
	})

	t.Run("Code: B plants a copy of A's Code in B's own tree", func(t *testing.T) {
		f := newMultiUserFixture(t, legacyIdentityLayout())
		copyRoot := f.Layout.CodePhysical(fixtureUserB, fixtureCodeFolder) // same folder name, B's tree
		f.put(copyRoot+"/product.json", forged("code", fixtureCodeID))
		f.put(copyRoot+"/workflow.json", `{"schema_version":1,"id":"`+fixtureCodeID+`","label":"App","capabilities":{}}`)
		// The folder is registered to A: B's copy is not B's project.
		if got, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, f.Code, fixtureCodeID, ""); err == nil {
			t.Fatalf("B opened a copy of A's registered Code: %+v", got)
		}
		// And no CLI starts for it: an explicit refusal before the launch, not a fallback.
		in := turnRunAsInput{ProfileID: "code", WorkingFolder: f.Layout.CodeShort(fixtureCodeFolder), CallerID: fixtureUserB, CLIWorkingDir: "/x/cli", SharedWorkingDir: "/x/cli"}
		if runAs, err := declareTurnRunAs(f.Ctx(fixtureUserB), in); err == nil || !errors.Is(err, errProjectOwnerMismatch) {
			t.Fatalf("B's launch on a copy of A's Code was not refused: %+v err=%v", runAs, err)
		}
		// A's own launch is unaffected.
		inA := turnRunAsInput{ProfileID: "code", WorkingFolder: f.Layout.CodeShort(fixtureCodeFolder), CallerID: fixtureUserA, CLIWorkingDir: "/x/cli", SharedWorkingDir: "/x/cli"}
		if runAs, err := declareTurnRunAs(f.Ctx(fixtureUserA), inA); err != nil || runAs.Slot != fixtureSlotA {
			t.Fatalf("A's launch: %+v err=%v", runAs, err)
		}
	})

	for name, layout := range map[string]identityLayout{"legacy per-user folder": legacyIdentityLayout(), "Crew/<id>": sharedIdentityLayout()} {
		layout := layout
		t.Run("Crew ("+name+"): the owner edits owner_id to B", func(t *testing.T) {
			f := newMultiUserFixture(t, layout)
			crewA := layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
			if got := f.Proxy(fixtureUserA, http.MethodPut, crewA+"/product.json"); got != 0 {
				t.Fatalf("the proxy refused the owner's own manifest write: %d", got)
			}
			if got := f.Proxy(fixtureUserB, http.MethodPut, crewA+"/product.json"); got != http.StatusForbidden {
				t.Fatalf("the proxy let B write A's Crew manifest: %d", got)
			}
			f.put(crewA+"/product.json", forged("work", fixtureCrewID))
			if got := resolveProjectOwner(f.Ctx(fixtureUserA), crewA); got != fixtureUserA {
				t.Fatalf("resolved owner = %q after the edit", got)
			}
			ref, ok := resolveCrewPath(f.Ctx(fixtureUserA), fixtureUserA, crewA)
			if !ok || ref.OwnerID != fixtureUserA {
				t.Fatalf("resolveCrewPath owner = %+v ok=%v", ref, ok)
			}
			if crewAccessFor(f.Claims(fixtureUserA), ref) != crewAccessOwner {
				t.Fatal("A lost ownership of their Crew by editing the manifest")
			}
			if crewAccessFor(f.Claims(fixtureUserB), ref) == crewAccessOwner {
				t.Fatal("B became the owner by a manifest edit")
			}
			// Raw proxy: still A's alone.
			if got := f.Proxy(fixtureUserB, http.MethodGet, crewA+"/code/notes.md"); got != http.StatusForbidden {
				t.Fatalf("B reached A's Crew raw after the edit: %d", got)
			}
			if got := f.Proxy(fixtureUserA, http.MethodGet, crewA+"/code/notes.md"); got != 0 {
				t.Fatalf("A lost raw access to their Crew: %d", got)
			}
			// A Crew turn never runs as B's slot, before or after (PLAT-446: the app account).
			for _, caller := range []string{fixtureUserA, fixtureUserB} {
				in := turnRunAsInput{ProfileID: "work", WorkingFolder: crewA, CallerID: caller, CLIWorkingDir: "/x/cli", SharedWorkingDir: "/x/cli-shared"}
				if runAs := decideTurnRunAs(f.Ctx(caller), in, defaultRunAsDeps()); runAs.Slot == fixtureSlotB {
					t.Fatalf("a Crew turn of %s selected B's slot", caller)
				}
			}
		})
	}
}
