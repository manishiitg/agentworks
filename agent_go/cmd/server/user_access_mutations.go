package server

import (
	"net/http"
	"sync"
	"time"
)

// Serialize complete directory read/modify/write operations, including sign-in
// updates, so an admin check and its durable receipt describe the saved file.
var userDirectoryMutationMu sync.Mutex

type userAccessMutationKey struct{}

// Only the external users dispatcher can supply this context; client JSON
// cannot bypass confirmation or manufacture a successful retry receipt.
type userAccessMutation struct {
	Key, Hash, Confirm, RequestID, Fields string
}

type userAccessReceipt struct {
	Hash      string        `json:"hash"`
	CreatedAt time.Time     `json:"created_at"`
	Result    userAdminView `json:"result"`
}

const userAccessRetryWindow = 7 * 24 * time.Hour

func pruneUserAccessReceipts(dir *userDirectory, now time.Time) {
	for key, receipt := range dir.AccessRequests {
		if !receipt.CreatedAt.After(now.Add(-userAccessRetryWindow)) {
			delete(dir.AccessRequests, key)
		}
	}
}

func activeDirectoryAdmin(rec *UserRecord) bool {
	return rec != nil && !rec.Disabled && accessForRecord(rec).Admin
}

func directoryHasActiveAdmin(dir *userDirectory) bool {
	for i := range dir.Users {
		if activeDirectoryAdmin(&dir.Users[i]) {
			return true
		}
	}
	return false
}

func userAccessMutationCallerAllowed(r *http.Request, dir *userDirectory) bool {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		return false
	}
	if caller := dir.find(claims.UserID, claims.Username, claims.Email); caller != nil {
		return activeDirectoryAdmin(caller)
	}
	if userAccessForClaims(claims).Known {
		return false
	}
	// Keep the app's existing local-installation/legacy owner bootstrap path.
	return currentUserIsAdmin(r)
}
