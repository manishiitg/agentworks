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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// cliHostOS is the platform the agent server runs on (a variable for tests).
var cliHostOS = runtime.GOOS

// cliPersonalMac says whether the server runs on a person's own Mac (macOS,
// single-user). There coding CLIs run Full CLI under Seatbelt with the
// person's home open, folder-guard grants may name absolute host folders, and
// the Code terminal runs as the person. Every other host, and any multi-user
// server, confines its CLIs with Landlock or runs them bridge-only (PLAT-364,
// PLAT-394). There is no switch: the platform decides, so local runs and
// servers never drift on a forgotten setting.
func cliPersonalMac() bool {
	return cliHostOS == "darwin" && !IsMultiUserMode()
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
// it gets Full CLI under Seatbelt. Everywhere else it is confined to the
// folders its folder guard grants, with a private CLI home in its working
// directory, and gets Full CLI inside that lock. If neither lock can be
// applied the chat falls back to bridge tools only; it never runs unconfined. The guard is only known after the agent
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
	cliRunConfined   cliRunDecision = "confined"    // Full CLI inside the Landlock lock
	cliRunBridgeOnly cliRunDecision = "bridge_only" // no native tools: no lock could be applied
	cliRunSeatbelt   cliRunDecision = "seatbelt"    // Full CLI inside macOS Seatbelt: a person's own Mac
)

// cliSeatbeltAvailable reports whether macOS sandbox-exec is present (a variable for tests).
var cliSeatbeltAvailable = func() bool {
	_, err := os.Stat("/usr/bin/sandbox-exec")
	return err == nil
}

// decideCLIConfinement is applyCLILandlock's choice, kept separate so it can
// be tested without an agent. Every CLI gets the same lock
// (multi-llm-provider-go wraps every adapter's launch); there is no unconfined
// answer.
func decideCLIConfinement(provider, workingDir string) (cliRunDecision, string, string) {
	if strings.TrimSpace(workingDir) == "" {
		return cliRunBridgeOnly, "", "the CLI has no working folder to confine it to"
	}
	if cliPersonalMac() {
		if !cliSeatbeltAvailable() {
			return cliRunBridgeOnly, "", "this Mac has no sandbox-exec to confine coding CLIs"
		}
		return cliRunSeatbelt, "", ""
	}
	runner, ok := cliLandlockRunner()
	if !ok {
		return cliRunBridgeOnly, "", "this host cannot confine coding CLIs (no working Landlock launcher)"
	}
	return cliRunConfined, runner, ""
}

// applyCLISeatbelt confines the CLI on a person's own Mac with sandbox-exec.
// The person's home stays open (their settings, logins, terminal config, other
// projects); AgentWorks' workspace data is closed except this chat's folder
// grants, the folder guard's blocked paths (planning/, the raw database) are
// refused, and opening or scripting other apps is blocked. The CLI keeps its
// real home (its Keychain login is tied to its config folder); PrivateHome only
// holds the profile. If the policy cannot be attached the chat runs bridge-only.
func applyCLISeatbelt(llmAgent *agent.LLMAgentWrapper, sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy) {
	policy := cliLandlockPolicyForSession(sessionID, provider, workingDir, base)
	policy.Mode = llmtypes.CLISecurityModeIsolated
	policy.Seatbelt = true
	policy.LandlockRunner = ""
	policy.ProtectedRoots = []string{fsutil.WorkspaceDocsRoot()}
	policy.PrivateHome = filepath.Join(workingDir, security.SandboxPersistentDirName, "cli-home", cliHomeName(provider))
	if err := llmAgent.SetCLISecurityPolicy(&policy); err != nil {
		failClosedToBridgeOnly(llmAgent, sessionID, fmt.Sprintf("the Seatbelt policy could not be attached (%v)", err))
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
		if !cliPersonalMac() {
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
