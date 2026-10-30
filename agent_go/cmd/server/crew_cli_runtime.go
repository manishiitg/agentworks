package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// One native-tool policy for ordinary Crew turns and explicit local resume.
func applyCrewNativeToolPolicy(profile *agentprofiles.Profile, owned, nativeTools bool) {
	if owned && nativeTools && profile.ToolPolicy.IsAllowlist() {
		profile.Runtime.AgentTools.Mode = "full"
	}
	if !owned {
		profile.Runtime.AgentTools.Mode = "mcp_only"
	}
}

func crewCLIMode(readOnly bool) string {
	if readOnly {
		return "run"
	}
	return "builder"
}

func crewCLIWorkingDir(folder, user, session, provider string, readOnly bool) (string, error) {
	if roots, bound, err := localCrewRoots(folder); bound || err != nil {
		return roots.Files, err
	}
	project := codingAgentWorkspaceWorkingDir(folder)
	if !isCodingAgentProvider(provider, "") {
		return project, nil
	}
	stateRoot, err := workflowCLIStateRoot()
	if err != nil {
		return "", fmt.Errorf("cannot isolate Crew CLI: %w", err)
	}
	// A Crew whose owner holds a slot runs its CLI as that slot (PLAT-872-84), from the slot's state folder
	// (slot_state_root/<slot>: the platform's and that slot's only, outside the documents; tmux already routes a
	// session started there to the slot's own server). Otherwise the app's private state, as before.
	// A slot without its state folder on this host keeps the app account, and so does a CLI whose server login
	// would reach the slot as a file (see slotLoginFromEnv).
	if slot, _ := crewOwnerSlot(folder); slot != "" && slotLoginFromEnv(provider) {
		if state, stateErr := slots.StateDirFor(slot); stateErr == nil {
			if info, statErr := os.Stat(state); statErr == nil && info.IsDir() {
				stateRoot = state
			}
		}
	}
	// A Crew that moved to the shared root keeps its runtime folder: the digest input stays the path the Crew had when
	// its chats began (the server's registry records it), so the native CLI sessions of its chats survive the move.
	legacyRel := ""
	if shared, ok := workspaceref.MustParse(strings.TrimSpace(folder)).SharedProject(); ok {
		legacyRel = crewLegacyKey(shared)
	}
	dir, err := cliruntime.PrepareLinkedProjectMoved(stateRoot, fsutil.WorkspaceDocsRoot(), user, project, legacyRel, session, provider, crewCLIMode(readOnly))
	if err != nil {
		return "", fmt.Errorf("cannot isolate Crew CLI: %w", err)
	}
	if err := prepareCrewGit(dir, project, provider); err != nil {
		return "", err
	}
	return dir, nil
}

func prepareCrewGit(runtimeDir, project, provider string) error {
	if cliHostOS != "linux" {
		return nil // A personal Mac keeps its own HOME and Git configuration.
	}
	if err := cliruntime.PrepareProjectGit(filepath.Clean(runtimeDir), cliHomeName(provider), project); err != nil {
		return fmt.Errorf("cannot prepare Crew Git: %w", err)
	}
	return nil
}

func crewCLIWorkspaceInstructions(folder string) string {
	if roots, bound, err := localCrewRoots(folder); bound && err == nil {
		return fmt.Sprintf("\nThis Crew works directly in %q. Native file tools and terminal commands start there. Crew skills, memory, knowledge and reports live in .agentworks/; platform settings, functions, database and chat records stay in private AgentWorks storage. use %q as their resource root. Workspace file tools use %q for the person's files and %q for Crew resources. Keep generated provider configuration and private session data in the supplied private runtime. Preserve the project's existing AGENTS.md, CLAUDE.md and provider configuration.\n", roots.Files, roots.Resources, roots.FilesPath(), roots.Virtual)
	}
	return fmt.Sprintf("\nCrew CLI runtime: your current directory holds this session's mode-specific instructions, skills and CLI configuration. `project/` links to the real Crew project at %q. Read project files through `project/<path>`; in Builder, create and edit durable files there too. For commands that require the project as their working directory, cd to that absolute path (`cd <that path> && ...`), never `cd project`, which the CLI's permission check refuses; do not put project outputs in the runtime root. Search and glob tools do not look inside the link unless you name it: always pass `project` (or `project/<folder>`) as the search path, because a search from the current directory finds none of the project's files. Workspace bridge paths remain relative to the real Crew project and must not include `project/`. Keep generated CLI instructions/configuration in the runtime root, preserve the project's own instructions, and obey this session's Run/Builder permissions even when reading project guidance.\n", codingAgentWorkspaceWorkingDir(folder)) + linkedProjectRuntimeSkillInstructions
}

func crewPrivateRuntimeDir(folder, user, session, provider string, readOnly bool) (string, error) {
	roots, bound, err := localCrewRoots(folder)
	if err != nil || !bound {
		return "", err
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	dir, err := cliruntime.PrepareMoved(state, fsutil.WorkspaceDocsRoot(), user, roots.Private, roots.Virtual, session, provider, crewCLIMode(readOnly))
	if err == nil {
		err = prepareCrewGit(dir, roots.Files, provider)
	}
	return dir, err
}

// applyCrewChatMode runs after target admission, before fingerprinting or skill
// loading. Access and the downgrade-only pin choose the prompt and skill set;
// client workshop_mode cannot promote a reader or guest.
func applyCrewChatMode(profile *resolvedAgentProfile, req *QueryRequest, readOnly bool) error {
	if profile == nil || !isProjectProfileID(profile.Definition.ID) {
		return nil
	}
	definition := profile.Definition
	oldSkills := definition.Skills
	manifest, err := workproduct.WorkManifest()
	if err != nil {
		return err
	}
	modeSkills := manifest.Chat[crewCLIMode(readOnly)].Skills
	selected := make([]string, 0, len(req.SelectedSkills))
	for _, name := range req.SelectedSkills {
		if name != "crew-run" && name != "crew-builder" {
			selected = append(selected, name)
		}
	}
	req.SelectedSkills = selected
	if readOnly {
		definition.SystemPromptTemplate = workproduct.RunPromptTemplate()
		definition.Runtime.AgentTools.Mode = "mcp_only"
		// Keep project/domain skills, but replace the platform's authoring
		// bundle with the reader contract. These skills guide, never authorize.
		removed := map[string]bool{"crew-builder": true, "crew-run": true}
		for _, name := range oldSkills {
			removed[name] = true
		}
		selected = make([]string, 0, len(req.SelectedSkills))
		for _, name := range req.SelectedSkills {
			if !removed[name] {
				selected = append(selected, name)
			}
		}
		req.SelectedSkills = selected
		definition.Skills = append([]string(nil), modeSkills...)
	} else {
		definition.Skills = appendUniqueStrings(append([]string(nil), oldSkills...), modeSkills...)
	}
	req.SelectedSkills = appendUniqueStrings(req.SelectedSkills, modeSkills...)
	rendered, err := agentprofiles.RenderPrompt(definition, req.AgentProfileContext)
	if err != nil {
		return fmt.Errorf("render Crew %s prompt: %w", crewCLIMode(readOnly), err)
	}
	profile.Definition = definition
	profile.Prompt = strings.TrimSpace(rendered) + profile.AgentRolePrompt
	if profile.Publication != nil {
		if profile.Publication.AgentID != "" {
			profile.Prompt += "\n\nYour published source is " + profile.Publication.Name + ". Its instructions and working data are separate from Main. Use the Crew's existing resources within this turn's permissions."
		}
		profile.Prompt += "\n\n" + publishedSourceNotice + "\nThis published agent saves working data under " + profile.Publication.runtimeRoot() + ". Use AGENTWORKS_PUBLICATION_RUNTIME in scripts for working data; imported source is read-only."
		if publishedBuilderHandoff(req, readOnly) {
			profile.Prompt += "\n\nPrivate Builder handoff summary:\n" + publicationHistorySummary(profile.Publication)
		}
	}
	return nil
}

// crewOwnerSlot is the slot the Crew's owner holds (from the server's registry) and that owner; "" when the folder
// is not a shared Crew, its owner is unknown or holds no slot, or slots are off.
func crewOwnerSlot(folder string) (slot, owner string) {
	shared, ok := workspaceref.MustParse(strings.TrimSpace(folder)).SharedProject()
	if !ok {
		return "", ""
	}
	owner = resolveProjectOwner(context.Background(), workspaceref.SharedProjectPath(shared))
	if owner == "" {
		return "", ""
	}
	return slotHeldBy(owner), owner
}

// inSlotStateFolder reports whether dir lies inside the slot's state folder.
func inSlotStateFolder(dir, slot string) bool {
	state, err := slots.StateDirFor(slot)
	if err != nil || state == "" {
		return false
	}
	return strings.HasPrefix(filepath.Clean(dir)+string(filepath.Separator), filepath.Clean(state)+string(filepath.Separator))
}

// slotLoginFromEnv reports whether provider's CLI can run as a person's slot with the server's shared login kept
// off the slot's files: only Claude Code takes it from an environment variable (CLAUDE_CODE_OAUTH_TOKEN or
// ANTHROPIC_API_KEY) and never writes it out. Codex writes an API key into its home's auth.json, and Cursor and Muse
// keep file logins, which the CLI's shells could read; they stay on the app account until that changes (PLAT-872-84).
func slotLoginFromEnv(provider string) bool {
	// Off for now: the Claude Agent SDK launch runs its node bridge as the slot, and slotctl's allow-list does not
	// list node yet ("slotctl: refused: /usr/bin/node is not an allowed program"), so every Claude Crew turn as a
	// slot failed. Back on once the host allows the bridge.
	_ = provider
	return false
}
