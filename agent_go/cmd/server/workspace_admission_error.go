package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
)

var errWorkspaceUnavailable = errors.New("workspace service unavailable")

func retryableWorkspaceFailure(err error) bool {
	var network net.Error
	return errors.Is(err, errWorkspaceUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &network)
}

// Only pre-dispatch admission calls this. A transport failure is not evidence
// of denied ownership, and is safe to retry because no model input was sent.
func writeWorkspaceAdmissionUnavailable(w http.ResponseWriter, err error) bool {
	if !retryableWorkspaceFailure(err) {
		return false
	}
	w.Header().Set("X-Query-Refusal-Reason", "workspace_unavailable")
	log.Printf("[QUERY_ADMISSION] submission=%s action=retryable reason=workspace_unavailable status=503", w.Header().Get("X-Submission-ID"))
	externalError(w, http.StatusServiceUnavailable, "workspace_unavailable", "The workspace is temporarily unavailable. Try again.")
	return true
}
