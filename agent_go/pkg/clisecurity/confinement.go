package clisecurity

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ResolveWorkflowPolicy is installed by the serving host. It resolves the final
// step guard at the common child-agent factory, not at individual run callers.
var ResolveWorkflowPolicy func(context.Context, string, string, string) (*llmtypes.CLISecurityPolicy, error)

type Decision string

const (
	PersonalWindows Decision = "personal_windows"
	Confined        Decision = "confined"
	BridgeOnly      Decision = "bridge_only"
	Seatbelt        Decision = "seatbelt"
)

// Host supplies machine paths; policy decisions and guard translation are shared
// by Crew chats and workflow execution agents.
type Host struct {
	PersonalMac, PersonalWindows bool
	// Execution guards explicitly grant their run/output areas, including Crew private runs.
	WorkflowExecution         bool
	SeatbeltAvailable         bool
	LandlockRunner            func() (string, bool)
	ResolvePath               func(string) string
	LookupRoots               func(string) (crewroot.Roots, bool, error)
	ProtectedRoots            []string
	DownloadsFolder, DocsRoot string
}

func (h Host) Decide(workingDir string) (Decision, string, string) {
	if strings.TrimSpace(workingDir) == "" {
		return BridgeOnly, "", "the CLI has no working folder to confine it to"
	}
	if h.PersonalWindows {
		return PersonalWindows, "", ""
	}
	if h.PersonalMac {
		if !h.SeatbeltAvailable {
			return BridgeOnly, "", "this Mac has no sandbox-exec to confine coding CLIs"
		}
		return Seatbelt, "", ""
	}
	runner, ok := h.LandlockRunner()
	if !ok {
		return BridgeOnly, "", "this host cannot confine coding CLIs (no working Landlock launcher)"
	}
	return Confined, runner, ""
}

func (h Host) Policy(sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy, runtimeDir ...string) (*llmtypes.CLISecurityPolicy, Decision, string) {
	decision, runner, why := h.Decide(workingDir)
	if decision == BridgeOnly {
		return nil, decision, why
	}
	if decision == PersonalWindows {
		return &llmtypes.CLISecurityPolicy{Provider: provider, Mode: llmtypes.CLISecurityModeCompatibility, ApprovedCapabilities: []string{"personal_windows"}}, decision, ""
	}
	policy := h.Grants(sessionID, provider, workingDir, base, runtimeDir...)
	policy.Provider = provider
	policy.Mode = llmtypes.CLISecurityModeIsolated
	policy.PrivateHome = PrivateHome(provider, workingDir, runtimeDir...)
	policy.Seatbelt = decision == Seatbelt
	policy.LandlockRunner = runner
	if decision == Seatbelt {
		policy.ProtectedRoots = append([]string(nil), h.ProtectedRoots...)
		if len(runtimeDir) > 0 && runtimeDir[0] != "" {
			policy.ProtectedRoots = append(policy.ProtectedRoots, workingDir)
		}
	}
	return &policy, decision, ""
}

func PrivateHome(provider, workingDir string, runtimeDir ...string) string {
	return filepath.Join(ArtifactRoot(workingDir, runtimeDir...), security.SandboxPersistentDirName, "cli-home", HomeName(provider))
}
func ArtifactRoot(workingDir string, runtimeDir ...string) string {
	if len(runtimeDir) > 0 && runtimeDir[0] != "" {
		return runtimeDir[0]
	}
	return workingDir
}
func appendUnique(values []string, more ...string) []string {
	for _, v := range more {
		found := false
		for _, old := range values {
			if old == v {
				found = true
				break
			}
		}
		if !found {
			values = append(values, v)
		}
	}
	return values
}

func (h Host) Grants(sessionID, provider, workingDir string, base *llmtypes.CLISecurityPolicy, runtimeDir ...string) llmtypes.CLISecurityPolicy {
	policy := llmtypes.CLISecurityPolicy{Provider: provider}
	if base != nil {
		policy = base.Clone()
	}
	cfg := common.GetSessionShellConfig(sessionID)
	readOnly := cfg != nil && (cfg.CrewReader || cfg.WorkflowReadOnly)
	if readOnly {
		policy.WorkspaceWritePaths = nil
		// A Crew Run-mode turn may write its run folder, and nothing else of the project (PLAT-756).
		if cfg.CrewReader && cfg.RunOutputPath != "" {
			if path := h.ResolvePath(cfg.RunOutputPath); path != "" {
				policy.WorkspaceWritePaths = append(policy.WorkspaceWritePaths, path)
			}
		}
	}
	policy.WorkspaceWritePaths = appendUnique(policy.WorkspaceWritePaths, ArtifactRoot(workingDir, runtimeDir...))
	if cfg != nil {
		for _, rel := range cfg.ReadPaths {
			if path := h.ResolvePath(rel); path != "" {
				policy.WorkspaceReadPaths = appendUnique(policy.WorkspaceReadPaths, path)
			}
			if roots, found, err := h.LookupRoots(strings.TrimSuffix(rel, "/")); err == nil && found && strings.TrimSuffix(rel, "/") == roots.Virtual {
				policy.WorkspaceReadPaths = appendUnique(policy.WorkspaceReadPaths, roots.Private)
				for _, name := range []string{"db.sqlite", "db.sqlite-wal", "db.sqlite-shm", "db.sqlite-journal"} {
					policy.BlockedPaths = appendUnique(policy.BlockedPaths, filepath.Join(roots.Private, "db", name))
				}
			}
		}
		if !readOnly {
			for _, rel := range cfg.WritePaths {
				// Chats write user resources; execution children also retain their
				// dedicated run/output grants in separated Crew storage.
				if roots, found, err := h.LookupRoots(strings.TrimSuffix(rel, "/")); err == nil && found {
					suffix := strings.TrimPrefix(strings.TrimPrefix(rel, roots.Virtual), "/")
					if !h.WorkflowExecution && roots.ResourcePathRoot(suffix) == roots.Private {
						continue
					}
				}
				if path := h.ResolvePath(rel); path != "" {
					policy.WorkspaceWritePaths = appendUnique(policy.WorkspaceWritePaths, path)
				}
			}
		}
		for _, rel := range cfg.BlockedPaths {
			if path := h.ResolvePath(rel); path != "" {
				policy.BlockedPaths = appendUnique(policy.BlockedPaths, path)
			}
		}
		for _, rel := range cfg.BlockedWritePaths {
			if path := h.ResolvePath(rel); path != "" {
				policy.BlockedWritePaths = appendUnique(policy.BlockedWritePaths, path)
			}
		}
	}
	// The person removed their Mac's Downloads folder: blocked last, so it wins over any grant.
	if h.PersonalMac && common.HostDownloadsRemoved.Load() {
		if downloads := h.DownloadsFolder; downloads != "" {
			policy.BlockedPaths = appendUnique(policy.BlockedPaths, downloads)
		}
	}
	policy.WorkspaceWritePaths = crewroot.SyncWriteGrants(h.DocsRoot, policy.WorkspaceWritePaths)
	return policy
}

func HomeName(provider string) string {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "cli"
	}
	return name
}
