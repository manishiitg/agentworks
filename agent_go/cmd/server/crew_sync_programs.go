package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Paths and account selection come from the destination's normal admission.
// A packet's workspace is descriptive only and never selects a storage root.
func (api *StreamingAPI) crewProgramStore(ctx context.Context, claims *UserClaims, roots crewroot.Roots, c ProductConversationRecord, transport string, importing bool) (store, runtimeDir string, err error) {
	if claims == nil || roots.Owner != claims.UserID || claims.UserID == "" {
		return "", "", fmt.Errorf("only the project owner can move its native session")
	}
	runtimeDir, err = crewPrivateRuntimeDir(roots.Virtual, claims.UserID, c.SessionID, c.Provider, false)
	if err == nil && runtimeDir == "" {
		runtimeDir, err = crewCLIWorkingDir(roots.Virtual, claims.UserID, c.SessionID, c.Provider, false)
	}
	if err != nil || runtimeDir == "" {
		return "", "", fmt.Errorf("the private agent runtime is unavailable")
	}
	if c.Provider == "muse-cli" {
		if !cliPersonalMac() {
			// The agent can write inside its runtime, so none of the journal-home
			// components may redirect an application-side import to another home.
			store = runtimeDir
			for _, part := range []string{security.SandboxPersistentDirName, "cli-home", "muse-cli", ".local", "share"} {
				store = filepath.Join(store, part)
				info, e := os.Lstat(store)
				if e != nil && !os.IsNotExist(e) {
					return "", "", e
				}
				if e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
					return "", "", fmt.Errorf("the private Muse home contains a link or non-directory")
				}
			}
			return store, runtimeDir, nil
		}
		id := strings.TrimSpace(c.ConnectionID)
		if id != "" && !strings.HasPrefix(id, "global:") {
			_, err = api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"}, "muse-cli", id)
			if err != nil {
				return "", "", err
			}
			home, e := providerConnectionHome(id)
			return filepath.Join(home, ".local", "share"), runtimeDir, e
		}
		if store = strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); store == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return "", "", e
			}
			store = filepath.Join(home, ".local", "share")
		}
		return store, runtimeDir, nil
	}
	id := strings.TrimSpace(c.ConnectionID)
	if id == "" {
		id = "global:" + c.Provider
	}
	if _, err = api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"}, c.Provider, id); err != nil {
		return "", "", err
	}
	target, err := api.resolveProviderAccountTarget(ctx, id, c.Provider)
	if err != nil {
		return "", "", err
	}
	sandboxHome := cliNativeHomeEnvironment(c.Provider, runtimeDir)
	nativeEnv := append([]string{}, target.Env...)
	for key, value := range sandboxHome {
		nativeEnv = append(nativeEnv, key+"="+value)
	}
	if c.Provider == "cursor-cli" {
		if transport == "sdk" {
			store, err = sessiontransfer.PathDirectory(runtimeDir, security.SandboxPersistentDirName+"/cursor-sdk-state", importing)
			return
		}
		store, err = cursorcli.NativeRuntimeConfigDirectory(runtimeDir, nativeEnv)
		if err == nil && importing && crewroot.Within(runtimeDir, store) {
			rel, e := filepath.Rel(runtimeDir, store)
			if e != nil {
				return "", "", e
			}
			store, err = sessiontransfer.PathDirectory(runtimeDir, filepath.ToSlash(rel), true)
		}
		return
	}
	sub, key := ".claude", "CLAUDE_CONFIG_DIR"
	if c.Provider == "codex-cli" {
		sub, key = ".codex", "CODEX_HOME"
	}
	if confined := sandboxHome[key]; confined != "" {
		rel, e := filepath.Rel(runtimeDir, confined)
		if e != nil {
			return "", "", e
		}
		store, err = sessiontransfer.PathDirectory(runtimeDir, filepath.ToSlash(rel), importing)
		return
	}
	store = filepath.Join(target.Home, sub)
	for _, entry := range target.Env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key && strings.TrimSpace(v) != "" {
			store = v
		}
	}

	return
}

func (api *StreamingAPI) captureCrewNative(ctx context.Context, claims *UserClaims, roots crewroot.Roots, c ProductConversationRecord, runtime *ChatHistoryAgentRuntime) (*sessiontransfer.Bundle, error) {
	if runtime == nil || runtime.AgentSessionHandle == nil {
		return nil, nil
	}
	h := runtime.AgentSessionHandle.Provider
	if c.Provider != "claude-code" && c.Provider != "codex-cli" && c.Provider != "cursor-cli" && c.Provider != "muse-cli" {
		return nil, nil
	}
	cwd, e := crewCLIWorkingDir(roots.Virtual, claims.UserID, c.SessionID, c.Provider, false)
	if e != nil {
		return nil, e
	}
	if runtime.Provider != c.Provider || h.Provider != c.Provider || h.NativeSessionID == "" || runtime.ExternalSessionID != h.NativeSessionID || !pathidentity.Same(h.WorkingDir, cwd) {
		return nil, fmt.Errorf("the saved native identity does not match this conversation")
	}
	store, _, e := api.crewProgramStore(ctx, claims, roots, c, h.Transport, false)
	if e != nil {
		return nil, e
	}
	out, e := os.MkdirTemp(roots.Private, "native-export-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(out)
	b, e := exportCrewProgram(store, cwd, h.NativeSessionID, h.Provider, h.Transport, out, true)
	if e != nil {
		return nil, e
	}
	if e = stageCrewNative(roots, b); e != nil {
		return nil, e
	}
	return b, nil
}
func exportCrewProgram(store, cwd, id, provider, transport, out string, remember bool) (*sessiontransfer.Bundle, error) {
	if provider == "muse-cli" {
		return sessiontransfer.ExportMuse(store, id, cwd, transport)
	}
	if provider == "cursor-cli" {
		if transport == "sdk" {
			return sessiontransfer.ExportCursorSDK(store, cwd, id, out, remember)
		}
		if transport == "tmux" {
			return sessiontransfer.ExportCursorCLI(store, cwd, id, out, remember)
		}
		return nil, fmt.Errorf("this Cursor runtime has no portable native store; continued from saved conversation history")
	}
	return sessiontransfer.ExportJSONL(provider, transport, store, cwd, id, out)
}

func crewNativeRuntimeAvailable(ctx context.Context, b *sessiontransfer.Bundle) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.Transport == "sdk" {
		if err := llmtypes.PrepareCodingSDK(ctx, b.Provider, nil); err != nil {
			return err
		}
		if _, err := exec.LookPath("node"); err != nil {
			return fmt.Errorf("the agent component is unavailable; update AgentWorks")
		}
		if b.Provider == "claude-code" {
			return claudecode.SDKRuntimeAvailable()
		}
		return cursorcli.SDKRuntimeAvailable()
	}
	if b.Transport == "tmux" {
		if _, err := exec.LookPath("tmux"); err != nil {
			return fmt.Errorf("this computer cannot open the saved terminal agent; continued from saved conversation history")
		}
	}
	command := map[string]string{"claude-code": "claude", "codex-cli": "codex", "cursor-cli": "cursor-agent", "muse-cli": "muse"}[b.Provider]
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("this AI program is unavailable on this computer; continued from saved conversation history")
	}
	return nil
}

var errCrewNativeDestinationBusy = errors.New("the destination agent is still working; retry after it finishes")

func (api *StreamingAPI) importCrewNative(ctx context.Context, claims *UserClaims, roots crewroot.Roots, current ProductConversationRecord, session string, b *sessiontransfer.Bundle) (*ChatHistoryAgentRuntime, error) {
	if b == nil {
		return nil, nil
	}
	if current.Provider != b.Provider {
		return nil, fmt.Errorf("the destination AI program differs from this native session")
	}
	// Shared journal formats resume through this deployment's configured runtime.
	// Cursor's two native formats can only resume through their matching runtime.
	destination := *b
	if b.Provider == "claude-code" || b.Provider == "codex-cli" {
		if contract, ok := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(b.Provider), current.ModelID); ok {
			destination.Transport = string(contract.Transport)
		}
	}
	b = &destination
	if err := crewNativeRuntimeAvailable(ctx, b); err != nil {
		return nil, err
	}
	if api.crewNativeTurnRunning(roots) {
		return nil, errCrewNativeDestinationBusy
	}
	closeWorkflowPolicyCLI(current.SessionID, current.Provider, "resuming native session from other computer")
	// Fresh Linux runtimes must not hide an offline branch in the prior runtime.
	raw, readErr := ReadChatHistoryConversation(claims.UserID, current.SessionID, current.WorkspacePath)
	if readErr != nil && !errors.Is(readErr, errChatHistoryConversationNotFound) {
		return nil, readErr
	}
	if readErr == nil {
		var old struct {
			UserID  string                   `json:"user_id"`
			Runtime *ChatHistoryAgentRuntime `json:"runtime"`
		}
		if json.Unmarshal(raw, &old) != nil {
			return nil, fmt.Errorf("the prior native conversation could not be checked; both copies were kept")
		}
		if old.UserID != claims.UserID {
			return nil, fmt.Errorf("the prior native conversation belongs to another person")
		}
		if old.Runtime != nil && old.Runtime.AgentSessionHandle != nil && old.Runtime.ExternalSessionID == b.SessionID {
			h := old.Runtime.AgentSessionHandle.Provider
			if h.Provider != b.Provider || (b.Provider == "cursor-cli" && h.Transport != b.Transport) {
				return nil, fmt.Errorf("the saved native runtime differs on this computer")
			}
			priorStore, _, e := api.crewProgramStore(ctx, claims, roots, current, h.Transport, false)
			if e != nil {
				return nil, e
			}
			tmp, e := os.MkdirTemp(roots.Private, "native-previous-")
			if e != nil {
				return nil, e
			}
			defer os.RemoveAll(tmp)
			previous, e := exportCrewProgram(priorStore, h.WorkingDir, b.SessionID, b.Provider, b.Transport, tmp, false)
			if e != nil {
				return nil, e
			}
			if strings.HasSuffix(b.Format, "sqlite") {
				baseline := priorStore
				if b.Format == "cursor-cli-sqlite" {
					baseline, e = sessiontransfer.CursorCLIPath(priorStore, h.WorkingDir, b.SessionID)
				}
				if e == nil {
					e = sessiontransfer.CheckSnapshotReplacement(baseline, previous, b)
				}
			} else {
				e = sessiontransfer.Ancestor(previous, b)
			}
			if e != nil {
				return nil, e
			}
		}
	}
	next := current
	next.SessionID = session
	store, runtimeDir, e := api.crewProgramStore(ctx, claims, roots, next, b.Transport, true)
	if e != nil {
		return nil, e
	}
	cwd, e := crewCLIWorkingDir(roots.Virtual, claims.UserID, session, b.Provider, false)
	if e != nil {
		return nil, e
	}
	switch b.Format {
	case "muse-jsonl":
		e = sessiontransfer.ImportMuse(store, cwd, b)
	case "cursor-sdk-sqlite":
		e = sessiontransfer.ImportCursorSDK(store, cwd, b)
	case "cursor-cli-sqlite":
		e = sessiontransfer.ImportCursorCLI(store, cwd, b)
	default:
		e = sessiontransfer.ImportJSONL(store, cwd, b)
	}
	if e != nil {
		return nil, e
	}
	log.Printf("[CREW_HANDOFF] project=%s phase=import mode=native_candidate provider=%s transport=%s native_session=%s", roots.ID, b.Provider, b.Transport, b.SessionID)
	return &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: b.Provider, ModelID: current.ModelID, ExternalSessionID: b.SessionID, ResumeSupported: true, WorkspacePath: current.WorkspacePath, NativeHandoff: true, AgentToolsMode: current.handoffAgentToolsMode, WorkshopMode: normalizeChatHistoryWorkshopMode(crewCLIMode(false)), AgentSessionHandle: &mcpagent.AgentSessionHandle{ConnectionID: current.ConnectionID, SessionID: session, OwnerID: session, Scope: "coding_agent", Provider: llmtypes.CodingProviderSessionHandle{Provider: b.Provider, Transport: b.Transport, NativeFormat: b.Format, NativeSessionID: b.SessionID, WorkingDir: cwd, RuntimeDir: runtimeDir, ProjectDirID: cwd, Model: current.ModelID}}}, nil
}

// Source IDs are not portable authority. Keep the destination-selected account
// or use its admitted server account. Native packets never carry a login.
func (api *StreamingAPI) crewNativeDestination(ctx context.Context, claims *UserClaims, roots crewroot.Roots, current ProductConversationRecord, identity ProductAgentIdentity, sourceModel, provider string) (*ProductAgentIdentity, string) {
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		return nil, "This AI program is unavailable on this computer; continued from saved conversation history."
	}
	option, offered := findProviderOptionByID(profile.Runtime.ProviderOptions, provider)
	if !offered {
		return nil, "This AI program is unavailable on this computer; continued from saved conversation history."
	}
	account := ""
	if identity.Engine == option.ID {
		account = identity.ConnectionID
	} else if current.Provider == provider {
		account = current.ConnectionID
	} else if !IsLocalMode() {
		account = "global:" + provider
	} else {
		return nil, "Choose an AI program account on this computer to resume its native session; continued from saved conversation history."
	}
	if _, err := api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"}, provider, account); err != nil {
		return nil, "This AI program is not available to this account on this computer; continued from saved conversation history."
	}
	model := strings.TrimSpace(sourceModel)
	if model == "" && current.Provider == provider {
		model = current.ModelID
	}
	if model == "" {
		model = option.ModelID
	}
	if !providerOptionOffersModel(option, currentCodingAgentModel(provider, model)) || checkAccountModel(withModelLimitPerson(ctx, claims.UserID), provider, account, model) != nil {
		return nil, "This AI model is unavailable on this computer; continued from saved conversation history."
	}
	identity.Engine, identity.ModelID, identity.ConnectionID, identity.ReasoningEffort = option.ID, model, account, ""
	identity.UpdatedBy = claims.UserID
	identity.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return &identity, ""
}
