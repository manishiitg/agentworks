package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type crewFunctionSubmissionIndex struct {
	CallID      string `json:"call_id"`
	Fingerprint string `json:"fingerprint"`
	// SavedAt lets an old submission_id be reused: a record older than
	// crewFunctionSubmissionTTL no longer binds its ID (records saved before
	// this field existed keep binding).
	SavedAt string `json:"saved_at,omitempty"`
}

// crewFunctionSubmissionTTL is how long a submission_id keeps returning its
// original call. Long enough to cover any retry of an uncertain call, short
// enough that a recurring ID ("daily-status") starts fresh work each week.
const crewFunctionSubmissionTTL = 7 * 24 * time.Hour

func crewFunctionSubmissionExpired(index crewFunctionSubmissionIndex, now time.Time) bool {
	saved, err := time.Parse(time.RFC3339, index.SavedAt)
	return err == nil && now.Sub(saved) > crewFunctionSubmissionTTL
}

// callerChat scopes the ID to one chat when chats of a Code call each other.
func crewFunctionSubmissionPath(userID string, caller triggerCaller, submissionID, callerPath, callerChat string) string {
	parts := []string{userID, caller.Type, caller.ID, caller.ProfileID, submissionID}
	if caller.ProfileID == "code" {
		parts = append(parts, canonicalCrewWorkspaceRoot(callerPath))
	}
	if callerChat != "" {
		parts = append(parts, "chat", callerChat)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "_system/function_submissions/" + hex.EncodeToString(sum[:]) + ".json"
}

func crewFunctionSubmissionFingerprint(target triggerTarget, function, argsKey string) string {
	parts := []string{target.Kind, target.stampID(), function, argsKey}
	if target.CrewProfile == "code" {
		parts = append(parts, target.CrewProfile, target.Path)
	}
	if target.Chat != nil {
		parts = append(parts, "chat", target.Chat.Key)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func crewFunctionTargetChat(target triggerTarget) string {
	if target.Chat == nil {
		return ""
	}
	return target.Chat.Key
}

func crewFunctionArgumentsFingerprint(argsKey string) string {
	sum := sha256.Sum256([]byte(argsKey))
	return hex.EncodeToString(sum[:])
}

func inMemoryCrewFunctionSubmissionLocked(userID string, caller triggerCaller, callerPath, callerChat, submissionID string, target triggerTarget, function, argsKey string) (*crewFunctionCall, bool, error) {
	for _, call := range crewFunctionCalls.m {
		if call.UserID != userID || call.CallerChat != callerChat || crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) != crewFunctionKey(caller.Type, caller.ProfileID, caller.ID) || call.SubmissionID != submissionID {
			continue
		}
		if caller.ProfileID == "code" && canonicalCrewWorkspaceRoot(call.CallerPath) != canonicalCrewWorkspaceRoot(callerPath) {
			continue
		}
		if call.TargetKind != target.Kind || call.TargetID != target.stampID() || (target.CrewProfile == "code" && call.TargetPath != target.Path) || call.TargetChat != crewFunctionTargetChat(target) || call.Function != function || call.ArgumentsKey != crewFunctionArgumentsFingerprint(argsKey) {
			return nil, true, fmt.Errorf("submission_id already belongs to a different function call")
		}
		return call, true, nil
	}
	return nil, false, nil
}

func lookupCrewFunctionSubmission(ctx context.Context, userID string, caller triggerCaller, callerPath, callerChat, submissionID string, target triggerTarget, function, argsKey string) (*crewFunctionCall, bool, error) {
	path := crewFunctionSubmissionPath(userID, caller, submissionID, callerPath, callerChat)
	raw, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	var index crewFunctionSubmissionIndex
	if json.Unmarshal([]byte(raw), &index) != nil || !strings.HasPrefix(index.CallID, "fn-") {
		return nil, true, fmt.Errorf("submission_id has an invalid saved call; inspect before retrying")
	}
	if crewFunctionSubmissionExpired(index, time.Now()) {
		return nil, false, nil // expired: the ID starts a new call, and saving it replaces this record
	}
	if index.Fingerprint != crewFunctionSubmissionFingerprint(target, function, argsKey) {
		return nil, true, fmt.Errorf("submission_id already belongs to a different function call")
	}
	call := lookupCrewFunctionCall(index.CallID)
	if call == nil || call.UserID != userID || call.CallerChat != callerChat || crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) != crewFunctionKey(caller.Type, caller.ProfileID, caller.ID) || (caller.ProfileID == "code" && canonicalCrewWorkspaceRoot(call.CallerPath) != canonicalCrewWorkspaceRoot(callerPath)) || call.SubmissionID != submissionID ||
		call.TargetKind != target.Kind || call.TargetID != target.stampID() || call.TargetChat != crewFunctionTargetChat(target) || call.Function != function || call.ArgumentsKey != crewFunctionArgumentsFingerprint(argsKey) {
		return nil, true, fmt.Errorf("submission_id belongs to an uncertain call; inspect before retrying")
	}
	return call, true, nil
}

func saveCrewFunctionSubmission(ctx context.Context, call *crewFunctionCall, caller triggerCaller) error {
	index := crewFunctionSubmissionIndex{CallID: call.ID, Fingerprint: crewFunctionSubmissionFingerprint(call.target, call.Function, call.argsKey), SavedAt: time.Now().UTC().Format(time.RFC3339)}
	encoded, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, crewFunctionSubmissionPath(call.UserID, caller, call.SubmissionID, call.CallerPath, call.CallerChat), string(encoded)+"\n")
}
