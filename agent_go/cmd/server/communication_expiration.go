package server

import (
	"errors"
	"net/http"
)

var errCommunicationExpired = errors.New("expired")

// Only authorized, previously known resources return expired. Unknown IDs and
// another account's resources retain the existing non-disclosing refusal.
func externalCommunicationError(w http.ResponseWriter, status int, code string, err error) {
	if errors.Is(err, errCommunicationExpired) {
		status, code = http.StatusGone, "expired"
	}
	externalError(w, status, code, err.Error())
}
