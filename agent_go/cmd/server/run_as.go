package server

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-442 step 2: the platform names the account a coding CLI runs as, per turn.
//
// The provider used to guess it from the CLI's starting folder (`_users/<id>/...` ran as that user's slot). That
// worked for the folders in use and silently depended on where a project is stored. Now each turn's identity is
// decided here and declared to the provider (llmtypes.RunAs); the provider keeps the path rule only as a logged
// fallback for launches that declare nothing.
//
// What each turn runs as TODAY (verified in runAsRegressionTable, run_as_test.go; behaviour is unchanged):
//
//	Code, the owner's turn      the owner's slot: the CLI starts in the project folder inside the owner's tree.
//	Crew, owner or Run reader   the app account: a Crew CLI never starts in the Crew folder, it starts in an
//	                            isolated runtime folder under the app's state root (crewCLIWorkingDir) that links to
//	                            the project. The reader block, tools and folder guards limit the reader.
//	Goal (Workflow/<name>)      the app account (no slot): chats start in an isolated runtime, runs in Workflow/.
//	a private chat              the slot of the user whose tree it works in (the folder rule, now declared).
//
// The owner decided on 2026-10-04 that a Crew Run-mode reader's turn runs as the owner's slot. The CLI does not do
// that today (see PLAT-442, "Found"): this change keeps what happens and makes it explicit, it does not switch it.

// turnRunAsInput describes the turn whose CLI is about to start.
type turnRunAsInput struct {
	ProfileID string
	// WorkflowPhase is a Goal (workflow) builder or run chat.
	WorkflowPhase bool
	// WorkingFolder is the workspace-relative folder the turn works in (the Code or Crew project, the Goal, or the
	// caller's chats folder).
	WorkingFolder string
	// CLIWorkingDir is the folder the CLI really starts in; SharedWorkingDir is WorkingFolder on disk. They differ
	// when the CLI runs in an isolated runtime folder that links to the project.
	CLIWorkingDir    string
	SharedWorkingDir string
	CallerID         string
}

// runAsDeps are the lookups the decision needs; tests replace them.
type runAsDeps struct {
	// projectOwner is the owner of the Crew or Code at a physical project root (manifest first, path second).
	projectOwner func(ctx context.Context, root string) string
	// slotOf is the slot a user holds ("" for none, or when slots are off).
	slotOf func(user string) string
}

func defaultRunAsDeps() runAsDeps {
	return runAsDeps{projectOwner: resolveProjectOwner, slotOf: slotHeldBy}
}

// slotHeldBy is the slot account the host's table assigns to a user.
func slotHeldBy(user string) string {
	slot, enabled, err := slots.For(user)
	if err != nil || !enabled {
		return ""
	}
	return slot
}

// turnCLIWorkingDir is the folder a turn's CLI starts in and the turn's own folder on disk. A Goal chat and a Crew
// chat start in an isolated runtime folder under the app's state root that links to the project; every other turn
// starts in its own folder.
func turnCLIWorkingDir(profileID string, workflowPhase bool, folder, user, session, provider, workflowMode string, readOnly bool) (cliDir, sharedDir string, err error) {
	sharedDir = codingAgentWorkspaceWorkingDir(folder)
	switch {
	case workflowPhase:
		cliDir, err = workflowCLIWorkingDir(folder, user, session, provider, workflowMode)
	case profileID == crewProfileID:
		cliDir, err = crewCLIWorkingDir(folder, user, session, provider, readOnly)
	default:
		cliDir = sharedDir
	}
	return cliDir, sharedDir, err
}

// decideTurnRunAs is the one place that says whom a turn's CLI runs as.
func decideTurnRunAs(ctx context.Context, in turnRunAsInput, deps runAsDeps) llmtypes.RunAs {
	root := filepath.Clean(strings.TrimSpace(in.CLIWorkingDir))
	if strings.TrimSpace(in.CLIWorkingDir) == "" {
		root = ""
	}
	appAccount := llmtypes.RunAs{Declared: true, Root: root}
	owned := func(user string) llmtypes.RunAs {
		user = strings.TrimSpace(user)
		if user == "" {
			return appAccount
		}
		return llmtypes.RunAs{Declared: true, User: user, Slot: deps.slotOf(user), Root: root}
	}

	// A Goal never runs as a person's slot.
	if in.WorkflowPhase {
		return appAccount
	}
	// An isolated runtime folder belongs to the app account (app-owned, mode 0700): the CLI starts there, so it
	// runs there as the app account whatever project it links to.
	if in.CLIWorkingDir != "" && in.CLIWorkingDir != in.SharedWorkingDir {
		return appAccount
	}
	ref := workspaceref.MustParse(filepath.ToSlash(strings.TrimSpace(in.WorkingFolder)))
	if _, _, isProject := ref.Project(); isProject {
		// A Crew or Code: the owner comes from its manifest (path fallback), never from who is asking.
		switch {
		case strings.EqualFold(in.ProfileID, codeproduct.ProfileID), strings.EqualFold(in.ProfileID, crewProfileID):
			projectRoot, project, _ := ref.Project()
			physical := workspaceref.PhysicalPath(ref.Owner(), projectRoot, project)
			if !ref.HasOwner() {
				physical = workspaceref.PhysicalPath(in.CallerID, projectRoot, project)
			}
			return owned(deps.projectOwner(ctx, physical))
		}
	}
	// Anything else inside a user's own tree (a private chat) runs as that user; a folder that names no user
	// (a shared one) runs as the app account.
	if ref.HasOwner() {
		return owned(ref.Owner())
	}
	return appAccount
}

// declareTurnRunAs decides a turn's identity, declares it to the provider for the CLI's folder, and returns it
// for the launch policy to carry.
func declareTurnRunAs(ctx context.Context, in turnRunAsInput) llmtypes.RunAs {
	runAs := decideTurnRunAs(ctx, in, defaultRunAsDeps())
	llmtypes.DeclareRunAs(runAs.Root, runAs)
	log.Printf("[RUN_AS] %s turn in %s runs as %s", describeProfile(in.ProfileID, in.WorkflowPhase), runAs.Root, describeRunAs(runAs))
	return runAs
}

// declareFolderRunAs declares that a CLI starting in dir runs as user's slot (or the app account when the user
// holds none), for launches that are not a chat turn (a delegated sub-agent's runtime folder).
func declareFolderRunAs(dir, user string) {
	runAs := llmtypes.RunAs{Declared: true, User: user, Slot: slotHeldBy(user), Root: filepath.Clean(dir)}
	llmtypes.DeclareRunAs(runAs.Root, runAs)
	log.Printf("[RUN_AS] delegated sub-agent in %s runs as %s", runAs.Root, describeRunAs(runAs))
}

func describeProfile(profileID string, workflow bool) string {
	switch {
	case workflow:
		return "Goal"
	case profileID == "":
		return "chat"
	default:
		return profileID
	}
}

func describeRunAs(r llmtypes.RunAs) string {
	if r.User == "" {
		return "the app account"
	}
	if r.Slot == "" {
		return "user " + r.User + " (no slot: the app account)"
	}
	return "slot " + r.Slot + " of user " + r.User
}
