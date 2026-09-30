package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// cliLandlockEnv turns on confining coding CLIs with the Landlock launcher
// (PLAT-364): "on" for everyone, or "users:<id or email>,…" for a rollout to
// named users first. Off by default until each CLI is certified. It needs a
// Linux host whose launcher preflight passes, else CLIs run as before.
const cliLandlockEnv = "AGENTWORKS_CLI_LANDLOCK"

// cliFullEnv turns on Full CLI (the CLI's own shell and file edits) for
// confined chats: "on", or "users:<id or email>,…". It only ever applies on
// top of the Landlock lock and to chats with Native agent tools on.
const cliFullEnv = "AGENTWORKS_CLI_FULL"

// cliFullUnconfinedEnv turns on Full CLI without the lock ("on"), for a person's own machine
// (macOS has no launcher yet; Seatbelt is deferred, see PLAT-364). Claude then gets its own
// shell and file edits with the person's own rights, so it is refused on any multi-user server.
const cliFullUnconfinedEnv = "AGENTWORKS_CLI_FULL_UNCONFINED"

func cliFullUnconfinedAllowed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(cliFullUnconfinedEnv)), "on") && !IsMultiUserMode()
}

// applyFullUnconfined upgrades a Native-agent-tools chat to Full CLI without confinement.
func applyFullUnconfined(llmAgent *agent.LLMAgentWrapper, sessionID string) {
	if upgraded, err := llmAgent.UpgradeCodingAgentToolsToFullUnconfined(); err != nil {
		log.Printf("[CLI_LANDLOCK] session %s: could not turn on unconfined Full CLI: %v", sessionID, err)
	} else if upgraded {
		log.Printf("[CLI_LANDLOCK] session %s: Full CLI on WITHOUT confinement (%s=on, single-user machine)", sessionID, cliFullUnconfinedEnv)
	}
}

func cliLandlockRequested(userID, userEmail string) bool {
	return cliRolloutRequested(cliLandlockEnv, userID, userEmail)
}

func cliFullRequested(userID, userEmail string) bool {
	return cliRolloutRequested(cliFullEnv, userID, userEmail)
}

func cliRolloutRequested(env, userID, userEmail string) bool {
	value := strings.TrimSpace(os.Getenv(env))
	if strings.EqualFold(value, "on") {
		return true
	}
	list, ok := strings.CutPrefix(value, "users:")
	if !ok {
		return false
	}
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry != "" && (entry == userID || strings.EqualFold(entry, userEmail)) {
			return true
		}
	}
	return false
}

// applyCLILandlock confines the chat's coding CLI to the folders its folder
// guard grants, with a private CLI home in its working directory. The guard
// is only known after the agent wrapper is built, so the policy is replaced
// here, before the Agent is finalized. A read-only guard path stays read-only
// for the CLI; blocked paths inside a granted folder are not carved out
// (Landlock only adds access) — the bridge tools still enforce them.
func applyCLILandlock(llmAgent *agent.LLMAgentWrapper, userID, userEmail, sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy) {
	if llmAgent == nil {
		return
	}
	if !cliLandlockRequested(userID, userEmail) || strings.TrimSpace(workingDir) == "" {
		if cliFullUnconfinedAllowed() {
			applyFullUnconfined(llmAgent, sessionID)
		}
		return
	}
	runner, ok := security.CLILandlockRunner()
	if !ok {
		log.Printf("[CLI_LANDLOCK] %s=on but this host cannot confine CLIs (no Landlock launcher); session %s runs unconfined", cliLandlockEnv, sessionID)
		if cliFullUnconfinedAllowed() {
			applyFullUnconfined(llmAgent, sessionID)
		}
		return
	}
	policy := llmtypes.CLISecurityPolicy{Provider: provider}
	if base != nil {
		policy = base.Clone()
	}
	policy.Mode = llmtypes.CLISecurityModeIsolated
	policy.LandlockRunner = runner
	policy.PrivateHome = filepath.Join(workingDir, security.SandboxPersistentDirName, "cli-home", cliHomeName(provider))
	policy.WorkspaceWritePaths = appendUniqueStrings(policy.WorkspaceWritePaths, workingDir)
	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
		for _, rel := range cfg.ReadPaths {
			policy.WorkspaceReadPaths = appendUniqueStrings(policy.WorkspaceReadPaths, codingAgentWorkspaceWorkingDir(rel))
		}
		for _, rel := range cfg.WritePaths {
			policy.WorkspaceWritePaths = appendUniqueStrings(policy.WorkspaceWritePaths, codingAgentWorkspaceWorkingDir(rel))
		}
	}
	if err := llmAgent.SetCLISecurityPolicy(&policy); err != nil {
		log.Printf("[CLI_LANDLOCK] session %s: could not attach the Landlock policy: %v", sessionID, err)
		return
	}
	if cliFullRequested(userID, userEmail) {
		if upgraded, err := llmAgent.UpgradeCodingAgentToolsToFull(); err != nil {
			log.Printf("[CLI_LANDLOCK] session %s: could not turn on Full CLI: %v", sessionID, err)
		} else if upgraded {
			log.Printf("[CLI_LANDLOCK] session %s: Full CLI on (native shell and file edits, inside the lock)", sessionID)
		}
	}
	log.Printf("[CLI_LANDLOCK] session %s: %s confined (reads %d, writes %d, private home under %s)", sessionID, provider, len(policy.WorkspaceReadPaths), len(policy.WorkspaceWritePaths), workingDir)
}

func cliHomeName(provider string) string {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "cli"
	}
	return name
}
