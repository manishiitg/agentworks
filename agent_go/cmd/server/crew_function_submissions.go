package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type crewFunctionSubmissionIndex struct {
	CallID      string `json:"call_id"`
	Fingerprint string `json:"fingerprint"`
}

func crewFunctionSubmissionPath(userID string, caller triggerCaller, submissionID, callerPath string) string {
	parts := []string{userID, caller.Type, caller.ID, caller.ProfileID, submissionID}
	if caller.ProfileID == "code" {
		parts = append(parts, canonicalCrewWorkspaceRoot(callerPath))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "_system/function_submissions/" + hex.EncodeToString(sum[:]) + ".json"
}

func crewFunctionSubmissionFingerprint(target triggerTarget, function, argsKey string) string {
	parts := []string{target.Kind, target.stampID(), function, argsKey}
	if target.CrewProfile == "code" {
		parts = append(parts, target.CrewProfile, target.Path)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func crewFunctionArgumentsFingerprint(argsKey string) string {
	sum := sha256.Sum256([]byte(argsKey))
	return hex.EncodeToString(sum[:])
}

func inMemoryCrewFunctionSubmissionLocked(userID string, caller triggerCaller, callerPath, submissionID string, target triggerTarget, function, argsKey string) (*crewFunctionCall, bool, error) {
	for _, call := range crewFunctionCalls.m {
		if call.UserID != userID || crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) != crewFunctionKey(caller.Type, caller.ProfileID, caller.ID) || call.SubmissionID != submissionID {
			continue
		}
		if caller.ProfileID == "code" && canonicalCrewWorkspaceRoot(call.CallerPath) != canonicalCrewWorkspaceRoot(callerPath) {
			continue
		}
		if call.TargetKind != target.Kind || call.TargetID != target.stampID() || (target.CrewProfile == "code" && call.TargetPath != target.Path) || call.Function != function || call.ArgumentsKey != crewFunctionArgumentsFingerprint(argsKey) {
			return nil, true, fmt.Errorf("submission_id already belongs to a different function call")
		}
		return call, true, nil
	}
	return nil, false, nil
}

func lookupCrewFunctionSubmission(ctx context.Context, userID string, caller triggerCaller, callerPath, submissionID string, target triggerTarget, function, argsKey string) (*crewFunctionCall, bool, error) {
	path := crewFunctionSubmissionPath(userID, caller, submissionID, callerPath)
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
	if index.Fingerprint != crewFunctionSubmissionFingerprint(target, function, argsKey) {
		return nil, true, fmt.Errorf("submission_id already belongs to a different function call")
	}
	call := lookupCrewFunctionCall(index.CallID)
	if call == nil || call.UserID != userID || crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) != crewFunctionKey(caller.Type, caller.ProfileID, caller.ID) || (caller.ProfileID == "code" && canonicalCrewWorkspaceRoot(call.CallerPath) != canonicalCrewWorkspaceRoot(callerPath)) || call.SubmissionID != submissionID ||
		call.TargetKind != target.Kind || call.TargetID != target.stampID() || call.Function != function || call.ArgumentsKey != crewFunctionArgumentsFingerprint(argsKey) {
		return nil, true, fmt.Errorf("submission_id belongs to an uncertain call; inspect before retrying")
	}
	return call, true, nil
}

func saveCrewFunctionSubmission(ctx context.Context, call *crewFunctionCall, caller triggerCaller) error {
	index := crewFunctionSubmissionIndex{CallID: call.ID, Fingerprint: crewFunctionSubmissionFingerprint(call.target, call.Function, call.argsKey)}
	encoded, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, crewFunctionSubmissionPath(call.UserID, caller, call.SubmissionID, call.CallerPath), string(encoded)+"\n")
}
