package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectinstructions"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
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
	result, err := projectinstructions.Root(contract.NativeProjectInstructionFiles, nativeRoot, read)
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
