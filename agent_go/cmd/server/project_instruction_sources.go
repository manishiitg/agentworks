package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectinstructions"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

var projectInstructionLogKeys sync.Map

// Resolution uses the already-admitted turn and workspace route. Run turns keep
// their existing owner-slot guidance; instruction text grants no new access.
func resolveRootProjectInstructions(ctx context.Context, user, folder string, publication *publishedProject, provider, model string) (projectinstructions.RootResult, error) {
	resourceRoot := agentProfileRuntimeWorkspace(user, folder)
	root := crewFilesPath(resourceRoot)
	nativeRoot := false
	if publication != nil {
		root = publication.sourceRoot()
	} else if _, bound, err := localCrewRoots(folder); err != nil {
		return projectinstructions.RootResult{}, err
	} else {
		// crewCLIWorkingDir returns roots.Files only for a local bound Crew.
		// All linked/private runtimes conservatively keep explicit injection.
		nativeRoot = bound
	}
	contract, _ := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(provider), model)
	read := func(name string) (string, bool, error) {
		return readFileFromWorkspace(ctx, root+"/"+name)
	}
	native := contract.NativeProjectInstructionFiles
	// Claude Code 2.1.284 and later read AGENTS.md themselves; an older or
	// unreadable binary keeps it injected (a duplicate at worst, never a gap).
	if contract.Provider == llmproviders.ProviderClaudeCode && claudecode.ReadsAgentsMD() {
		native = append(native, "AGENTS.md")
	}
	result, err := projectinstructions.Root(native, nativeRoot, read)
	if err != nil {
		return result, err
	}
	// Imported folders can have their own shared rules separate from the
	// Crew resource-root rules. Preserve both; Crew rules are appended last.
	if publication == nil && root != resourceRoot {
		if content, found, readErr := read(projectinstructions.FileName); readErr != nil {
			return result, fmt.Errorf("cannot read project folder instructions: %w", readErr)
		} else if found {
			result.Section = strings.TrimSpace(result.Section + "\n\n" + projectinstructions.SourceSection("PROJECT_INSTRUCTIONS.md (project folder)", content))
			result.Files = append(result.Files, projectinstructions.FileStatus{Name: projectinstructions.FileName, Bytes: len(content), Method: "injected"})
			result.Key += ":" + projectinstructions.Key(content)
		}
	}
	// The Team part (#3041): the Team's TEAM_INSTRUCTIONS.md, synced to every copy of the Team's Crew and read
	// live here, after the Crew's own files. One log line per change shows a teammate's agent picked up an edit.
	// On the server it comes only from the Team's committed store (committedTeamInstructions); a Mac reads its own
	// synced copy, which only its own person and their agents can write.
	if publication == nil {
		var content string
		var found bool
		var readErr error
		if IsLocalMode() {
			content, found, readErr = read(projectinstructions.TeamFileName)
		} else if project := crewPanelProjectForPath(folder); project != "" {
			content, found = committedTeamInstructions(project)
		}
		if readErr != nil {
			return result, fmt.Errorf("cannot read the Team instructions: %w", readErr)
		} else if found {
			if section, cut := projectinstructions.TeamSection(content); section != "" {
				result.Section = strings.TrimSpace(result.Section + "\n\n" + section)
				result.Files = append(result.Files, projectinstructions.FileStatus{Name: projectinstructions.TeamFileName, Bytes: len(content), Method: "injected"})
				key := projectinstructions.Key(content)
				result.Key += ":team:" + key
				if previous, loaded := projectInstructionLogKeys.Swap(resourceRoot+":team", key); !loaded || previous != key {
					log.Printf("[TEAM_INSTRUCTIONS] workspace=%q event=applied bytes=%d key=%s cut=%v", resourceRoot, len(content), key[:12], cut)
				}
			}
		}
	}
	var summary strings.Builder
	for _, file := range result.Files {
		fmt.Fprintf(&summary, "%s:%s:%d ", file.Name, file.Method, file.Bytes)
	}
	// Only the latest content/method signature per Crew/provider is retained;
	// log once on discovery/change, never every turn and never file contents.
	logKey := resourceRoot + ":" + provider
	signature := result.Key + ":" + summary.String()
	previous, loaded := projectInstructionLogKeys.Swap(logKey, signature)
	if !loaded || previous != signature {
		log.Printf("[PROJECT_INSTRUCTIONS] workspace=%q provider=%q files=%q native_read=unverified", resourceRoot, provider, summary.String())
	}
	return result, nil
}
