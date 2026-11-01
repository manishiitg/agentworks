package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// The normal launch chooses the account and sandbox home. Reproduce that
// layout from our own admitted conversation binding, never a transferred path.
func (api *StreamingAPI) crewMuseStore(ctx context.Context, claims *UserClaims, roots crewroot.Roots, conversation ProductConversationRecord) (dataHome, runtimeDir string, err error) {
	if claims == nil || claims.UserID == "" || roots.Owner != claims.UserID {
		return "", "", fmt.Errorf("only the Crew owner can move its native session")
	}
	runtimeDir, err = crewPrivateRuntimeDir(roots.Virtual, claims.UserID, conversation.SessionID, "muse-cli", false)
	if err == nil && runtimeDir == "" {
		runtimeDir, err = crewCLIWorkingDir(roots.Virtual, claims.UserID, conversation.SessionID, "muse-cli", false)
	}
	if err != nil || runtimeDir == "" {
		return "", "", fmt.Errorf("the private Muse runtime is unavailable")
	}
	if !cliPersonalMac() {
		// The agent can write inside its runtime, so none of the journal-home
		// components may redirect an application-side import to another home.
		dataHome = runtimeDir
		for _, part := range []string{security.SandboxPersistentDirName, "cli-home", "muse-cli", ".local", "share"} {
			dataHome = filepath.Join(dataHome, part)
			info, e := os.Lstat(dataHome)
			if e != nil && !os.IsNotExist(e) {
				return "", "", e
			}
			if e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return "", "", fmt.Errorf("the private Muse home contains a link or non-directory")
			}
		}
		return dataHome, runtimeDir, nil
	}
	id := strings.TrimSpace(conversation.ConnectionID)
	if id != "" && !strings.HasPrefix(id, "global:") {
		_, err = api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"}, "muse-cli", id)
		if err != nil {
			return "", "", err
		}
		home, e := providerConnectionHome(id)
		return filepath.Join(home, ".local", "share"), runtimeDir, e
	}
	if dataHome = strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", "", e
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return dataHome, runtimeDir, nil
}

func (api *StreamingAPI) captureCrewMuse(ctx context.Context, claims *UserClaims, roots crewroot.Roots, conversation ProductConversationRecord, runtime *ChatHistoryAgentRuntime) (*musecli.NativeSessionBundle, error) {
	if runtime == nil || runtime.Provider != "muse-cli" || conversation.Provider != "muse-cli" || runtime.AgentSessionHandle == nil {
		return nil, nil
	}
	handle := runtime.AgentSessionHandle
	workingDir, err := crewCLIWorkingDir(roots.Virtual, claims.UserID, conversation.SessionID, "muse-cli", false)
	if err != nil {
		return nil, err
	}
	if handle.Provider.Provider != "muse-cli" || handle.Provider.NativeSessionID == "" || runtime.ExternalSessionID != handle.Provider.NativeSessionID || !pathidentity.Same(handle.Provider.WorkingDir, workingDir) {
		return nil, fmt.Errorf("the saved Muse identity does not match this conversation")
	}
	dataHome, _, err := api.crewMuseStore(ctx, claims, roots, conversation)
	if err != nil {
		return nil, err
	}
	return musecli.ExportNativeSession(dataHome, handle.Provider.NativeSessionID, workingDir)
}

func (api *StreamingAPI) importCrewMuse(ctx context.Context, claims *UserClaims, roots crewroot.Roots, current ProductConversationRecord, session string, bundle *musecli.NativeSessionBundle) (*ChatHistoryAgentRuntime, error) {
	if bundle == nil || current.Provider != "muse-cli" {
		return nil, nil
	}
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	// Linux uses a fresh private runtime at each handoff. Still check the old
	// destination copy before installing elsewhere, so divergence is not hidden
	// by the new directory. The Mac's normal store gets the same check on import.
	if raw, err := ReadChatHistoryConversation(claims.UserID, current.SessionID, current.WorkspacePath); err == nil {
		var old struct {
			UserID  string                   `json:"user_id"`
			Runtime *ChatHistoryAgentRuntime `json:"runtime"`
		}
		if json.Unmarshal(raw, &old) == nil && old.UserID == claims.UserID && old.Runtime != nil && old.Runtime.ExternalSessionID == bundle.SessionID {
			previous, e := api.captureCrewMuse(ctx, claims, roots, current, old.Runtime)
			if e != nil {
				return nil, e
			}
			if previous != nil {
				if e = musecli.NativeSessionAncestor(previous, bundle); e != nil {
					return nil, e
				}
			}
		}
	}
	next := current
	next.SessionID = session
	dataHome, runtimeDir, err := api.crewMuseStore(ctx, claims, roots, next)
	if err != nil {
		return nil, err
	}
	workingDir, err := crewCLIWorkingDir(roots.Virtual, claims.UserID, session, "muse-cli", false)
	if err != nil {
		return nil, err
	}
	if err = musecli.ImportNativeSession(dataHome, workingDir, bundle); err != nil {
		return nil, err
	}
	log.Printf("[CREW_HANDOFF] project=%s phase=import mode=native_candidate provider=muse-cli native_session=%s", roots.ID, bundle.SessionID)
	return &ChatHistoryAgentRuntime{
		Kind: "coding_agent", Provider: "muse-cli", ModelID: current.ModelID, ExternalSessionID: bundle.SessionID, ResumeSupported: true,
		WorkspacePath: current.WorkspacePath, NativeHandoff: true, AgentToolsMode: current.handoffAgentToolsMode, WorkshopMode: normalizeChatHistoryWorkshopMode(crewCLIMode(false)),
		AgentSessionHandle: &mcpagent.AgentSessionHandle{ConnectionID: current.ConnectionID, SessionID: session, OwnerID: session, Scope: "coding_agent", Provider: llmtypes.CodingProviderSessionHandle{
			Provider: "muse-cli", NativeSessionID: bundle.SessionID, WorkingDir: workingDir, RuntimeDir: runtimeDir, Model: current.ModelID,
		}},
	}, nil
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
