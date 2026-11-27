package server

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// queryAdmissionError is a refusal from admitQueryTarget.
type queryAdmissionError struct {
	err            error
	invalidProfile bool
}

func (e *queryAdmissionError) Error() string {
	if e.invalidProfile {
		return "Invalid agent profile request: " + e.err.Error()
	}
	return e.err.Error()
}

func (e *queryAdmissionError) Unwrap() error { return e.err }

// admitQueryTarget resolves a turn's target and the caller's access to it:
// the agent profile and concrete workspace, then
// conversationTargetAccess. handleQuery and the bot dry run
// (bot_dry_run.go) share it, so a dry run admits exactly what a real turn
// admits.
func (api *StreamingAPI) admitQueryTarget(ctx context.Context, req *QueryRequest, currentUserID, sessionID string) (*resolvedAgentProfile, WorkflowAccessLevel, *queryAdmissionError) {
	if product, _, ok := projectProductForPath(req.SelectedFolder); ok && req.AgentProfileID != "" && !strings.EqualFold(strings.TrimSpace(req.AgentProfileID), product.ProfileID) {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: fmt.Errorf("project folder requires its matching conversation profile"), invalidProfile: true}
	}
	resolvedProfile, err := api.resolveAgentProfileForQuery(ctx, req, currentUserID, sessionID)
	if err != nil {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: err, invalidProfile: true}
	}
	if isRetiredGeneralChat(req, resolvedProfile, sessionID) {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: errRetiredGeneralChat, invalidProfile: true}
	}
	// Only the verified Work binding may enter another person's Crew. A
	// profile-less workflow payload must not bypass Crew access/tool policy.
	if isCrewProjectPath(req.SelectedFolder) && !crewProjectOwnedByCaller(currentUserID, req.SelectedFolder) && !isProjectProfileID(req.AgentProfileID) {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: fmt.Errorf("another person's project requires its verified Work conversation")}
	}
	access, err := api.conversationTargetAccess(ctx, *req)
	if err != nil {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: err}
	}
	// Only targets actually checked above can authorize an item sender. A
	// profile-less folder alias must not inherit the account-tier fallback.
	req.notificationItemWorkspace = ""
	if resolvedProfile != nil {
		req.notificationItemWorkspace = personalConnectionItemRoot(currentUserID, req.SelectedFolder)
	} else if strings.HasPrefix(req.SelectedFolder, "Workflow/") {
		if _, exists, readErr := ReadWorkflowManifest(ctx, req.SelectedFolder); readErr == nil && exists {
			req.notificationItemWorkspace = personalConnectionItemRoot(currentUserID, req.SelectedFolder)
		}
	}
	req.notificationMailConnectionID, req.notificationMailBlockedRecipients = "", nil
	if req.notificationItemWorkspace != "" && strings.HasPrefix(req.SelectedFolder, "Workflow/") {
		manifest, found, err := ReadWorkflowManifest(ctx, req.SelectedFolder)
		if err != nil || !found {
			return nil, WorkflowAccessNone, &queryAdmissionError{err: fmt.Errorf("This workflow's mail settings are unavailable; open its project and try again.")}
		}
		req.notificationMailConnectionID = manifest.Capabilities.GmailConnectionID
		req.notificationMailBlockedRecipients = append([]string(nil), manifest.Capabilities.GmailBlockedRecipients...)
	}
	crewReadOnly := readOnlyForRequest(access, *req) || crewGuestCallerForTurn(*req, currentUserID) != ""
	// The server selects grants only after resolving and authorizing the project.
	// Browser hints and automated callers never add a laptop connection.
	req.LaptopConnection = nil
	caller := GetUserFromContext(ctx)
	// Why a turn has no laptop tools: on server B the tools came and went between turns, relaunching the CLI each
	// time (2026-10-28), and the log could not say which check failed.
	laptopWhy := fmt.Sprintf("read_only=%t website=%t builder_turn=%t project=%t", crewReadOnly, websiteDeviceClaims(caller), laptopBuilderTurn(*req, resolvedProfile), laptopProjectAllowed(ctx, caller, req.SelectedFolder))
	if !crewReadOnly && websiteDeviceClaims(caller) && laptopBuilderTurn(*req, resolvedProfile) && laptopProjectAllowed(ctx, caller, req.SelectedFolder) {
		settings, err := readLaptopSettings(currentUserID, req.SelectedFolder)
		laptopWhy += fmt.Sprintf(" settings_err=%v device=%t folders=%d", err, (&laptopConnectionTarget{DeviceID: settings.DeviceID}).valid(), len(settings.Folders))
		if err == nil && (&laptopConnectionTarget{DeviceID: settings.DeviceID}).valid() && len(settings.Folders) > 0 {
			req.LaptopConnection = &laptopConnectionTarget{DeviceID: settings.DeviceID}
			if err := api.validateLaptopConnection(caller, req.LaptopConnection); err != nil {
				return nil, WorkflowAccessNone, &queryAdmissionError{err: err, invalidProfile: true}
			}
			if resolvedProfile != nil {
				resolvedProfile.LaptopConnection = req.LaptopConnection
				resolvedProfile.LaptopConnectionPolicy = api.laptopConnectionPolicyKey(caller, req.LaptopConnection, crewReadOnly, req.SelectedFolder)
			}
		}
	}
	if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
		log.Printf("[LAPTOP_ACCESS] session=%s actor=%s project=%q local_tools=%t %s", sessionID, currentUserID, req.SelectedFolder, req.LaptopConnection != nil, laptopWhy)
	}
	if err := applyCrewChatMode(resolvedProfile, req, crewReadOnly); err != nil {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: err, invalidProfile: true}
	}
	return resolvedProfile, access, nil
}
