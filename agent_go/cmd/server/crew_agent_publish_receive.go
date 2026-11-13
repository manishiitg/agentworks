package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewAgentPublishAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Identity string `json:"identity,omitempty"`
	// Kind is whose account it is: "own" (the person's) or "company" (the server's, or one shared with them).
	Kind string `json:"kind,omitempty"`
}

type crewAgentPublishPreview struct {
	Ready       bool                      `json:"ready"`
	Message     string                    `json:"message,omitempty"`
	NeedsLogin  bool                      `json:"needs_login,omitempty"`
	Selection   crewAgentPublishSelection `json:"selection"`
	Accounts    []crewAgentPublishAccount `json:"accounts"`
	TransferID  string                    `json:"transfer_id,omitempty"`
	Files       int                       `json:"files"`
	Bytes       int64                     `json:"bytes"`
	NativeBytes int64                     `json:"native_bytes"`
	Callers     []string                  `json:"callers"`
	Warnings    []string                  `json:"warnings,omitempty"`
}

func (api *StreamingAPI) crewAgentPublishAccounts(ctx context.Context, claims *UserClaims, roots crewroot.Roots, provider, account string) (*crewAgentPublishPreview, error) {
	out := &crewAgentPublishPreview{Accounts: []crewAgentPublishAccount{}}
	server := "this server"
	if address, err := url.Parse(strings.TrimSpace(os.Getenv("PUBLIC_URL"))); err == nil && address.Host != "" {
		server = address.Host
	}
	program := providerAccountProductName(provider)
	if !providerEnabled(provider) {
		alternatives := []string{}
		for _, candidate := range getSupportedProviders() {
			if crewAgentPublishProgramSupported(candidate) {
				alternatives = append(alternatives, providerAccountProductName(candidate))
			}
		}
		out.Message = program + " isn't available on " + server + ". Ask its admin to turn it on"
		if len(alternatives) > 0 {
			out.Message += ", or switch this agent to an available program (" + strings.Join(alternatives, ", ") + ") first"
		}
		out.Message += "."
		return out, nil
	}
	// Your own account or one the company provides (owner, 13 Nov, crew_server_account.go).
	options, err := api.crewServerAccountOptions(ctx, claims, roots, provider)
	if err != nil {
		return nil, err
	}
	out.Accounts = options
	if account == "" {
		account = crewServerAccountPick(roots.ID, claims.UserID, provider, options)
	}
	if account == "" && len(options) == 1 {
		account = options[0].ID
	}
	if len(options) == 0 {
		out.NeedsLogin = true
		out.Message = "There is no " + program + " account you can use on " + server + ": sign in once in Providers → Add my account, or ask the admin to give you the company's account, then retry."
		return out, nil
	}
	if account == "" {
		out.Message = "Ask the person which account this agent uses on " + server + ", their own or the company's, then retry with account_id: " + crewServerAccountChoiceLine(options) + "."
		return out, nil
	}
	if !slices.ContainsFunc(options, func(o crewAgentPublishAccount) bool { return o.ID == account }) {
		return nil, fmt.Errorf("that account is not one you can use on %s; choose one of: %s", server, crewServerAccountChoiceLine(options))
	}
	admitted, err := api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"}, provider, account)
	if err != nil {
		return nil, err
	}
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return nil, err
	}
	if err = claudeCodeQueryAuthenticationError(&resolvedAgentProfile{Definition: profile}, provider, admitted); err != nil {
		return nil, err
	}
	if readCrewServerAccounts(roots.ID, claims.UserID)[provider] != account {
		if err = writeCrewServerAccount(roots.ID, claims.UserID, provider, account); err != nil {
			return nil, err
		}
		logCrewServerAccount(roots.ID, claims.UserID, provider, account, "run_online")
	}
	out.Ready, out.Selection.AccountID = true, account
	return out, nil
}

func crewAgentPublishProgramSupported(provider string) bool {
	return provider == "claude-code" || provider == "codex-cli" || provider == "cursor-cli" || provider == "muse-cli" || provider == "pi-cli" || provider == "agy-cli"
}

func (api *StreamingAPI) receiveCrewAgentPublish(ctx context.Context, claims *UserClaims, id string, in crewSyncRequest) (crewSyncReply, error) {
	roots, binding, err := api.crewTransferBinding(ctx, claims, id, in.PairID)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: id, Binding: binding}
	if IsLocalMode() || binding.Editing != "local" || binding.PendingAction != "" || binding.ArchiveOnly {
		return out, fmt.Errorf("publish from the Mac to its connected server with native-session support")
	}
	if in.PublishAgent == nil {
		return out, fmt.Errorf("choose an agent to publish")
	}
	selection, err := cleanCrewAgentPublishSelection(*in.PublishAgent)
	if err != nil {
		return out, err
	}
	provider := in.Provider
	if len(in.Native) == 1 && in.Native[0] != nil {
		provider = in.Native[0].Provider
	}
	if !crewAgentPublishProgramSupported(provider) {
		return out, fmt.Errorf("this program has no supported native-session transfer")
	}
	out.Publish, err = api.crewAgentPublishAccounts(ctx, claims, roots, provider, selection.AccountID)
	if err != nil {
		return out, err
	}
	if in.Action == "publish_accounts" {
		return out, nil
	}
	if !out.Publish.Ready {
		return out, fmt.Errorf("%s", out.Publish.Message)
	}
	if selection.AccountID != out.Publish.Selection.AccountID {
		return out, fmt.Errorf("confirm the server account before publishing")
	}
	if len(in.Native) != 1 || in.Native[0] == nil {
		return out, fmt.Errorf("publish requires exactly one native session")
	}
	if err = in.Native[0].Validate(); err != nil {
		return out, err
	}
	if err = crewNativeRuntimeAvailable(ctx, in.Native[0]); err != nil {
		return out, err
	}
	if in.Action == "stage_agent" {
		return api.prepareCrewTransfer(ctx, claims, id, in)
	}
	placement, err := crewPublishedAgent(id, selection.Tab)
	if err != nil {
		return out, err
	}
	if placement != nil {
		if placement.TransferID != in.TransferID {
			return out, fmt.Errorf("this agent already has a publish in progress or is on the server")
		}
		if placement.Location == "server" {
			out.Placements, err = readCrewAgentPlacements(id)
			return out, err
		}
	}
	if err = verifyCrewTransferDescriptor(id, binding.PairID, in.TransferID, in.Snapshot); err != nil {
		return out, err
	}
	if err = hydrateCrewPayloads(id, binding.PairID, in.TransferID, "upload", in.Snapshot); err != nil {
		return out, err
	}
	defer dropCrewPayloadBytes(in.Snapshot)
	if in.Snapshot.StatePath == "" || in.Snapshot.Git != nil || in.Snapshot.DatabasePath != "" {
		return out, fmt.Errorf("publish this agent's selected files and native session only")
	}
	main, err := api.crewHandoffProjectTemplate(ctx, claims, id)
	if err != nil {
		return out, err
	}
	if err = validateCrewMoveData(in.Snapshot.StatePath, binding.PeerProjectID, id, main.ConversationKey); err != nil {
		return out, err
	}
	var imported *crewMoveChat
	err = readCrewMoveData(in.Snapshot.StatePath, func(h crewMoveHeader, chat *crewMoveChat) error {
		if h.AgentTab != selection.Tab || h.AccountID != selection.AccountID || (h.MainKey != "" && !ordinaryProjectChatKey(h.SourceID, h.MainKey)) || len(h.Settings) != 0 {
			return fmt.Errorf("agent records differ from the confirmed publish")
		}
		if chat == nil {
			before := len(h.Schedules) + len(h.Functions) + len(h.Webhooks) + len(h.Callers)
			if err := filterCrewAgentCallers(&h); err != nil {
				return err
			}
			if before != len(h.Schedules)+len(h.Functions)+len(h.Webhooks)+len(h.Callers) {
				return fmt.Errorf("publish includes another agent's callers")
			}
			return nil
		}
		if imported != nil || chat.Previous || chat.Key != crewAgentPublishKey(h.SourceID, selection.Tab) || chat.Conversation == nil || chat.Conversation.Native == nil {
			return fmt.Errorf("publish must contain only this agent's current native conversation")
		}
		a, _ := json.Marshal(chat.Conversation.Native)
		b, _ := json.Marshal(in.Native[0])
		if !bytes.Equal(a, b) {
			return fmt.Errorf("the native session differs from the confirmed publish")
		}
		if chat.Conversation.Native.Provider != provider || chat.Conversation.Native.SessionID != in.Native[0].SessionID {
			return fmt.Errorf("the native session differs from the staged agent")
		}
		if selection.Tab != macMainTab && strings.TrimSpace(chat.Identity.Name) == "" {
			return fmt.Errorf("give the agent a name")
		}
		copy := *chat
		imported = &copy
		return nil
	})
	if err != nil {
		return out, err
	}
	if in.MacMainConversationID != "" && (!strings.HasPrefix(in.MacMainConversationID, "conversation-") || !productConversationKeyPattern.MatchString(in.MacMainConversationID)) {
		return out, fmt.Errorf("the Mac main conversation identity is missing")
	}
	if imported == nil {
		return out, fmt.Errorf("the agent's conversation is missing")
	}
	for key := range in.Snapshot.Files {
		if !crewAgentPublishIncludes(selection, key, false) {
			return out, fmt.Errorf("publish includes a file outside the selected scope")
		}
	}
	for _, change := range in.Snapshot.Changes {
		if !crewAgentPublishIncludes(selection, change.Path, false) {
			return out, fmt.Errorf("publish changes a file outside the selected scope")
		}
	}
	unlock, err := wf.LockWorkspace(ctx, roots.Files)
	if err != nil {
		return out, err
	}
	defer unlock()
	scoped, err := crewAgentScopedRoots(roots, selection.Tab)
	if err != nil {
		return out, err
	}
	// The conversation importer derives this same session from the admitted pair
	// and transfer. Placement is present before import so its CLI gets this root.
	importTransfer := in.TransferID + "/" + imported.Conversation.ConversationID
	session := "product-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(claims.UserID+"/"+id+"/"+binding.PairID+"/"+importTransfer)).String()
	entry := crewAgentPlacement{Name: firstNonEmptyTrimmed(imported.Identity.Name, "Main"), MacMainConversationID: in.MacMainConversationID, Tab: selection.Tab, Location: "publishing", TransferID: in.TransferID, SessionID: session, ConversationID: imported.Conversation.ConversationID, PairID: binding.PairID, SplitFiles: in.SplitFiles, Selection: selection}
	if err = writeCrewAgentPlacement(id, entry); err != nil {
		return out, err
	}
	if err = applyCrewSyncSnapshot(ctx, scoped, binding.PairID+"/agent/"+selection.Tab+"/"+in.TransferID, in.Snapshot); err != nil {
		return out, err
	}
	ensureSharedCrewFolderAccess(strings.TrimPrefix(roots.Virtual, "Crew/"), roots.Owner)
	if err = api.importCrewMoveData(ctx, claims, roots, binding.PairID, in.TransferID, main, in.Snapshot.StatePath); err != nil {
		return out, err
	}
	entry.Location, entry.Since = "server", time.Now().UTC().Format(time.RFC3339)
	if err = writeCrewAgentPlacement(id, entry); err != nil {
		return out, err
	}
	out.Placements, err = readCrewAgentPlacements(id)
	log.Printf("[CREW_AGENT_PUBLISH] owner=%s project=%s tab=%s transfer=%s native=%s files=%d result=server", claims.UserID, id, selection.Tab, in.TransferID, imported.Conversation.Native.SessionID, len(in.Snapshot.Files))
	return out, err
}
