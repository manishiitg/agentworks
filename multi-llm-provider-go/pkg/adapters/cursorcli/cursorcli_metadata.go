package cursorcli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func cursorMetaString(opts *llmtypes.CallOptions, key string) string {
	if opts.Metadata == nil {
		return ""
	}
	v, _ := opts.Metadata.Custom[key].(string)
	return strings.TrimSpace(v)
}

func cursorMetaBool(opts *llmtypes.CallOptions, key string) bool {
	if opts.Metadata == nil {
		return false
	}
	v, _ := opts.Metadata.Custom[key].(bool)
	return v
}

func cursorPlanFailure(text, model string) error {
	// Cursor emits this standalone account refusal as a normal message chunk.
	// Do not classify ordinary explanations or quoted discussions of plans.
	if strings.TrimSpace(text) != "Upgrade your plan to continue" {
		return nil
	}
	return &llmerrors.Error{Kind: llmerrors.KindBilling, Provider: "cursor-cli", Model: model,
		Err: fmt.Errorf("This Cursor account needs a plan upgrade to continue. Check its plan or select another Cursor account.")}
}

// Cursor can end a turn "successfully" whose whole reply is a dumped transport error. The turn failed: show it so,
// not as an answer. Only a reply made of nothing but such lines (and their stack frames) counts; an explanation that
// quotes the same text is an answer.
var (
	// Go has no lookahead: a RetriableError whose text starts [internal] is an agent-loop failure, checked apart.
	cursorTransportLine = regexp.MustCompile(`^Error: (?:RetriableError: .+|ConnectError: \[(?:unavailable|aborted|deadline_exceeded)\].*)$`)
	cursorStackFrame    = regexp.MustCompile(`^\s+at\s`)
)

const cursorServerErrorReply = "Something went wrong communicating with the server. Please try again."

func cursorTransportFailure(text, model string) error {
	failed := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t\r")
		switch {
		case strings.TrimSpace(line) == "":
		case line == cursorServerErrorReply || (cursorTransportLine.MatchString(line) && !strings.HasPrefix(line, "Error: RetriableError: [internal]")):
			failed = true
		case failed && cursorStackFrame.MatchString(line):
		default:
			return nil
		}
	}
	if !failed {
		return nil
	}
	return &llmerrors.Error{Kind: llmerrors.KindNetwork, Provider: "cursor-cli", Model: model,
		Err: fmt.Errorf("Cursor could not reach its server. Try again in a moment.")}
}
