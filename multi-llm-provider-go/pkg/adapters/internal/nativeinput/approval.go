package nativeinput

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// Only the displayed copy is masked; the provider retains the original input.
var approvalSecretAssignment = regexp.MustCompile(`(?i)\b([A-Z_][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY|ACCESS_KEY)[A-Z0-9_]*|TOKEN|SECRET|PASSWORD|API_KEY)=(?:"[^"\n]*"|'[^'\n]*'|[^\s;]+)`)

var approvalAuthorization = regexp.MustCompile(`(?i)(authorization\s*:\s*)(?:(?:bearer|basic)\s+)?[^\s"']+`)
var approvalSecretFlag = regexp.MustCompile(`(?i)((?:^|\s)--(?:password|token)(?:=|\s+))(?:"[^"\n]*"|'[^'\n]*'|[^\s;]+)`)
var approvalShortPassword = regexp.MustCompile(`((?:^|\s)-p)(?:"[^"\n]+"|'[^'\n]+'|[^\s;'"=]+)`)
var approvalURLPassword = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/\s]+:)[^@\s]+@`)

func approvalDisplay(description string) string {
	description = approvalSecretAssignment.ReplaceAllString(description, "$1=[hidden]")
	description = approvalAuthorization.ReplaceAllString(description, "$1[hidden]")
	description = approvalSecretFlag.ReplaceAllString(description, "$1[hidden]")
	description = approvalShortPassword.ReplaceAllString(description, "$1[hidden]")
	return approvalURLPassword.ReplaceAllString(description, "$1[hidden]@")
}

// ApproveCommand uses the attended chat's existing scoped answer endpoint.
// Deny is first because the generic card's "use defaults" must never grant
// command execution. Cancellation, missing attendance and expiry never allow.
func ApproveCommand(ctx context.Context, description string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	description = approvalDisplay(description)
	answers, err := AskWithKind(ctx, CommandApprovalRequest, []Question{{ID: "command_permission", Question: description,
		Options: []Option{{Label: "Deny"}, {Label: "Allow", Description: "Allow this command once."}}}})
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return len(answers) == 1 && len(answers[0].SelectedLabels) == 1 && strings.EqualFold(answers[0].SelectedLabels[0], "Allow"), nil
}

// ApprovalChoice is the person's answer to a command approval.
type ApprovalChoice int

const (
	ApprovalDeny ApprovalChoice = iota
	ApprovalOnce
	ApprovalAlways
)

// ApproveCommandChoice is ApproveCommand with a third answer when the agent offers a rule for the rest of
// the chat: "Allow for this chat" approves this command and ones like it until the chat's agent restarts.
func ApproveCommandChoice(ctx context.Context, description string, offerAlways bool) (ApprovalChoice, error) {
	if !offerAlways {
		allowed, err := ApproveCommand(ctx, description)
		if err != nil || !allowed {
			return ApprovalDeny, err
		}
		return ApprovalOnce, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	options := []Option{{Label: "Deny"}, {Label: "Allow", Description: "Allow this command once."},
		{Label: "Allow for this chat", Description: "Allow this command and ones like it until this chat's agent restarts."}}
	answers, err := AskWithKind(ctx, CommandApprovalRequest, []Question{{ID: "command_permission", Question: approvalDisplay(description), Options: options}})
	if err != nil {
		return ApprovalDeny, err
	}
	if err := ctx.Err(); err != nil {
		return ApprovalDeny, err
	}
	if len(answers) != 1 || len(answers[0].SelectedLabels) != 1 {
		return ApprovalDeny, nil
	}
	switch strings.ToLower(answers[0].SelectedLabels[0]) {
	case "allow":
		return ApprovalOnce, nil
	case "allow for this chat":
		return ApprovalAlways, nil
	}
	return ApprovalDeny, nil
}
