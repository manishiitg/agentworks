package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// cliHostOS is the platform the agent server runs on (a variable for tests).
var cliHostOS = runtime.GOOS

// cliUnconfinedAllowed says whether coding CLIs may run Full CLI (their own
// shell and file edits) without a lock: only on a person's own Mac, which has
// no Landlock (Seatbelt confinement is PLAT-364 follow-up work). Every other
// host, and any multi-user server, confines its CLIs (PLAT-364). There is no
// switch: the platform decides, so local runs and servers never drift on a
// forgotten setting.
func cliUnconfinedAllowed() bool {
	return cliHostOS == "darwin" && !IsMultiUserMode()
}

// applyFullUnconfined upgrades a Native-agent-tools chat to Full CLI without confinement.
func applyFullUnconfined(llmAgent *agent.LLMAgentWrapper, sessionID string) {
	if upgraded, err := llmAgent.UpgradeCodingAgentToolsToFullUnconfined(); err != nil {
		log.Printf("[CLI_LANDLOCK] session %s: could not turn on unconfined Full CLI: %v", sessionID, err)
	} else if upgraded {
		log.Printf("[CLI_LANDLOCK] session %s: Full CLI on WITHOUT confinement (a person's own Mac)", sessionID)
	}
}

// failClosedToBridgeOnly takes a chat's native tools away when its CLI cannot
// be confined. The chat keeps working through the bridge tools, where the
// folder guard checks every action; it never runs unconfined.
func failClosedToBridgeOnly(llmAgent *agent.LLMAgentWrapper, sessionID, why string) {
	if changed, err := llmAgent.RestrictCodingAgentToolsToMCPOnly(); err != nil {
		log.Printf("[CLI_LANDLOCK] SECURITY session %s: %s, and native tools could not be turned off: %v", sessionID, why, err)
	} else if changed {
		log.Printf("[CLI_LANDLOCK] SECURITY session %s: %s; native tools are OFF for this chat (bridge tools only)", sessionID, why)
	}
}

// applyCLILandlock decides how a chat's coding CLI runs. On a person's own Mac
// it gets Full CLI unconfined. Everywhere else it is confined to the folders
// its folder guard grants, with a private CLI home in its working directory,
// and gets Full CLI inside that lock; if the lock cannot be applied the chat
// falls back to bridge tools only. The guard is only known after the agent
// wrapper is built, so the policy is replaced here, before the Agent is
// finalized. A read-only guard path stays read-only for the CLI; blocked paths
// inside a granted folder are not carved out (Landlock only adds access), and
// the bridge tools still enforce them.
func applyCLILandlock(llmAgent *agent.LLMAgentWrapper, sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy) {
	if llmAgent == nil {
		return
	}
	decision, runner, why := decideCLIConfinement(provider, workingDir)
	switch decision {
	case cliRunUnconfined:
		applyFullUnconfined(llmAgent, sessionID)
		return
	case cliRunSeatbelt:
		applyCLISeatbelt(llmAgent, sessionID, provider, workingDir, base)
		return
	case cliRunBridgeOnly:
		failClosedToBridgeOnly(llmAgent, sessionID, why)
		return
	}
	policy := cliLandlockPolicyForSession(sessionID, provider, workingDir, base)
	policy.Mode = llmtypes.CLISecurityModeIsolated
	policy.LandlockRunner = runner
	policy.PrivateHome = filepath.Join(workingDir, security.SandboxPersistentDirName, "cli-home", cliHomeName(provider))
	if err := llmAgent.SetCLISecurityPolicy(&policy); err != nil {
		failClosedToBridgeOnly(llmAgent, sessionID, fmt.Sprintf("the Landlock policy could not be attached (%v)", err))
		return
	}
	// Blocked paths (planning/, the raw database, AGENTS.md) are enforced inside
	// the lock: multi-llm-provider-go splits the grants around them (PLAT-385).
	if upgraded, err := llmAgent.UpgradeCodingAgentToolsToFull(); err != nil {
		log.Printf("[CLI_LANDLOCK] session %s: could not turn on Full CLI: %v", sessionID, err)
	} else if upgraded {
		log.Printf("[CLI_LANDLOCK] session %s: Full CLI on (native shell and file edits, inside the lock)", sessionID)
	}
	log.Printf("[CLI_LANDLOCK] session %s: %s confined (reads %d, writes %d, private home under %s)", sessionID, provider, len(policy.WorkspaceReadPaths), len(policy.WorkspaceWritePaths), workingDir)
}

type cliRunDecision string

const (
	cliRunUnconfined cliRunDecision = "unconfined"  // Full CLI, no lock: a person's own Mac
	cliRunConfined   cliRunDecision = "confined"    // Full CLI inside the Landlock lock
	cliRunBridgeOnly cliRunDecision = "bridge_only" // no native tools: the lock could not be applied
	cliRunSeatbelt   cliRunDecision = "seatbelt"    // Full CLI inside macOS Seatbelt: a person's own Mac
)

// cliSeatbeltCertified are the CLIs whose Seatbelt launch is built and checked
// (multi-llm-provider-go wraps their launch). Others on a Mac run Full CLI
// unconfined until they are certified (PLAT-364).
var cliSeatbeltCertified = map[string]bool{"claude-code": true}

// cliSeatbeltAvailable reports whether macOS sandbox-exec is present (a variable for tests).
var cliSeatbeltAvailable = func() bool {
	_, err := os.Stat("/usr/bin/sandbox-exec")
	return err == nil
}

// decideCLIConfinement is applyCLILandlock's choice, kept separate so it can
// be tested without an agent. It never answers "unconfined" outside a
// person's own Mac.
func decideCLIConfinement(provider, workingDir string) (cliRunDecision, string, string) {
	if cliUnconfinedAllowed() {
		if cliSeatbeltCertified[strings.ToLower(strings.TrimSpace(provider))] && strings.TrimSpace(workingDir) != "" && cliSeatbeltAvailable() {
			return cliRunSeatbelt, "", ""
		}
		return cliRunUnconfined, "", ""
	}
	if strings.TrimSpace(workingDir) == "" {
		return cliRunBridgeOnly, "", "the CLI has no working folder to confine it to"
	}
	runner, ok := cliLandlockRunner()
	if !ok {
		return cliRunBridgeOnly, "", "this host cannot confine coding CLIs (no working Landlock launcher)"
	}
	return cliRunConfined, runner, ""
}

// applyCLISeatbelt confines the CLI on a person's own Mac with sandbox-exec:
// the same folder grants as the Linux lock, plus the folder guard's blocked
// paths (planning/, the raw database), which Seatbelt can refuse inside a
// granted folder. The CLI keeps its real home (its Keychain login is tied to
// its config folder); PrivateHome only holds the profile. If the policy cannot
// be attached the chat still runs Full CLI unconfined, as on any Mac before.
func applyCLISeatbelt(llmAgent *agent.LLMAgentWrapper, sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy) {
	policy := cliLandlockPolicyForSession(sessionID, provider, workingDir, base)
	policy.Mode = llmtypes.CLISecurityModeIsolated
	policy.Seatbelt = true
	policy.LandlockRunner = ""
	policy.PrivateHome = filepath.Join(workingDir, security.SandboxPersistentDirName, "cli-home", cliHomeName(provider))
	if err := llmAgent.SetCLISecurityPolicy(&policy); err != nil {
		log.Printf("[CLI_LANDLOCK] session %s: could not attach the Seatbelt policy (%v); Full CLI runs unconfined", sessionID, err)
		applyFullUnconfined(llmAgent, sessionID)
		return
	}
	if _, err := llmAgent.UpgradeCodingAgentToolsToFull(); err != nil {
		log.Printf("[CLI_LANDLOCK] session %s: could not turn on Full CLI: %v", sessionID, err)
	}
	log.Printf("[CLI_LANDLOCK] session %s: %s confined by Seatbelt (reads %d, writes %d, blocked %d+%d)", sessionID, provider, len(policy.WorkspaceReadPaths), len(policy.WorkspaceWritePaths), len(policy.BlockedPaths), len(policy.BlockedWritePaths))
}

// cliPolicyPath turns a folder-guard entry into the absolute path a sandbox
// policy needs: workspace-relative entries join the docs root. An absolute
// host grant (Downloads, a project folder) is kept only on a person's own Mac;
// on a server it is dropped ("" — the caller skips it), so a folder named in a
// workflow can never widen a CLI's sandbox beyond the workspace there.
func cliPolicyPath(path string) string {
	if filepath.IsAbs(strings.TrimSpace(path)) {
		if !cliUnconfinedAllowed() {
			return ""
		}
		return filepath.Clean(strings.TrimSpace(path))
	}
	return codingAgentWorkspaceWorkingDir(path)
}

// cliLandlockRunner finds the host's Landlock launcher (a variable for tests).
var cliLandlockRunner = security.CLILandlockRunner

// The final folder guard supplies project access. A read-only turn's writable
// runtime must never promote the linked real project through an initial grant
// or an attached folder alias. Landlock grants are additive, so dropping that
// write authority is essential; the link is not a read-only boundary itself.
func cliLandlockPolicyForSession(sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy) llmtypes.CLISecurityPolicy {
	policy := llmtypes.CLISecurityPolicy{Provider: provider}
	if base != nil {
		policy = base.Clone()
	}
	cfg := common.GetSessionShellConfig(sessionID)
	readOnly := cfg != nil && (cfg.CrewReader || cfg.WorkflowReadOnly)
	if readOnly {
		policy.WorkspaceWritePaths = nil
	}
	policy.WorkspaceWritePaths = appendUniqueStrings(policy.WorkspaceWritePaths, workingDir)
	if cfg != nil {
		for _, rel := range cfg.ReadPaths {
			if path := cliPolicyPath(rel); path != "" {
				policy.WorkspaceReadPaths = appendUniqueStrings(policy.WorkspaceReadPaths, path)
			}
		}
		if !readOnly {
			for _, rel := range cfg.WritePaths {
				if path := cliPolicyPath(rel); path != "" {
					policy.WorkspaceWritePaths = appendUniqueStrings(policy.WorkspaceWritePaths, path)
				}
			}
		}
		for _, rel := range cfg.BlockedPaths {
			if path := cliPolicyPath(rel); path != "" {
				policy.BlockedPaths = appendUniqueStrings(policy.BlockedPaths, path)
			}
		}
		for _, rel := range cfg.BlockedWritePaths {
			if path := cliPolicyPath(rel); path != "" {
				policy.BlockedWritePaths = appendUniqueStrings(policy.BlockedWritePaths, path)
			}
		}
	}
	return policy
}

func cliHomeName(provider string) string {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "cli"
	}
	return name
}
