package llmtypes

import "errors"

// ErrCodingAgentInputNotSent means the adapter rejected input before writing
// to the CLI because no native turn could receive it. It is safe to queue the
// input at an active outer turn boundary or start a new turn when idle.
var ErrCodingAgentInputNotSent = errors.New("coding agent input was not sent")

// ErrCodingAgentTurnEndedBeforeInputAck means a write was attempted, but its
// turn ended without a native input receipt. It is uncertain, NOT InputNotSent:
// callers need exact final-journal evidence before retrying the input.
var ErrCodingAgentTurnEndedBeforeInputAck = errors.New("coding agent turn ended before input acknowledgement")
